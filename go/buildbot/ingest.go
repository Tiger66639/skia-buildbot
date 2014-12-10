package buildbot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

var (
	// TODO(borenet): Avoid hard-coding this list. Instead, obtain it from
	// checked-in code or the set of masters which are actually running.
	MASTER_NAMES = []string{"client.skia", "client.skia.android", "client.skia.compile", "client.skia.fyi"}
	httpGet      = http.Get
)

// get loads data from a buildbot JSON endpoint.
func get(url string, rv interface{}) error {
	resp, err := httpGet(url)
	if err != nil {
		return fmt.Errorf("Failed to GET %s: %v", url, err)
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(rv); err != nil {
		return fmt.Errorf("Failed to decode JSON: %v", err)
	}
	return nil
}

// getBuildFromMaster retrieves the given build from the build master's JSON
// interface as specified by the master, builder, and build number.
func getBuildFromMaster(master, builder string, buildNumber int) (*Build, error) {
	var build Build
	url := fmt.Sprintf("%s%s/json/builders/%s/builds/%d", BUILDBOT_URL, master, builder, buildNumber)
	err := get(url, &build)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve build #%v for %v: %v", buildNumber, builder, err)
	}
	build.Branch = build.branch()
	build.GotRevision = build.gotRevision()
	build.MasterName = master
	slaveProp := build.GetProperty("slavename").([]interface{})
	if slaveProp != nil && len(slaveProp) == 3 {
		build.BuildSlave = slaveProp[1].(string)
	}
	build.Started = build.Times[0]
	build.Finished = build.Times[1]
	propBytes, err := json.Marshal(&build.Properties)
	if err != nil {
		return nil, fmt.Errorf("Unable to convert build properties to JSON: %v", err)
	}
	build.PropertiesStr = string(propBytes)

	// Set the master, builder, and buildNumber props on each step.
	for _, s := range build.Steps {
		s.MasterName = build.MasterName
		s.BuilderName = build.BuilderName
		s.BuildNumber = build.Number
		if len(s.ResultsRaw) > 0 {
			if s.ResultsRaw[0] == nil {
				s.ResultsRaw[0] = 0.0
			}
			s.Results = int(s.ResultsRaw[0].(float64))
		} else {
			s.Results = 0
		}
		s.Started = s.Times[0]
		s.Finished = s.Times[1]
	}

	// TODO(borenet): Actually find the commits for this build.
	build.Commits = []string{}

	return &build, nil
}

// GetBuildFromMaster retrieves the given build from the build master's JSON
// interface as specified by the master, builder, and build number. Makes
// multiple attempts in case the master fails to respond.
func GetBuildFromMaster(master, builder string, buildNumber int) (*Build, error) {
	var b *Build
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		b, err = getBuildFromMaster(master, builder, buildNumber)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return b, err
}

// IngestBuild retrieves the given build from the build master's JSON interface
// and pushes it into the database.
func IngestBuild(master, builder string, buildNumber int) error {
	b, err := GetBuildFromMaster(master, builder, buildNumber)
	if err != nil {
		return fmt.Errorf("Failed to load build from master: %v", err)
	}
	return b.ReplaceIntoDB()
}

// getLatestBuilds returns a map whose keys are master names and values are
// sub-maps whose keys are builder names and values are build numbers
// representing the newest build for each builder/master pair.
func getLatestBuilds() (map[string]map[string]int, error) {
	res := map[string]map[string]int{}
	errs := map[string]error{}
	type builder struct {
		CachedBuilds []int
	}
	var wg sync.WaitGroup
	for _, master := range MASTER_NAMES {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			builders := map[string]*builder{}
			err := get(BUILDBOT_URL+m+"/json/builders", &builders)
			if err != nil {
				errs[m] = fmt.Errorf("Failed to retrieve builders for %v: %v", m, err)
				return
			}
			myMap := map[string]int{}
			for name, b := range builders {
				if len(b.CachedBuilds) > 0 {
					myMap[name] = b.CachedBuilds[len(b.CachedBuilds)-1]
				}
			}
			if len(myMap) > 0 {
				res[m] = myMap
			}
		}(master)
	}
	wg.Wait()
	if len(errs) != 0 {
		return nil, fmt.Errorf("Encountered errors while loading builder data from masters: %v", errs)
	}
	return res, nil
}

// getUningestedBuilds returns a map whose keys are master names and values are
// sub-maps whose keys are builder names and values are slices of ints
// representing the numbers of builds which have not yet been ingested.
func getUningestedBuilds() (map[string]map[string][]int, error) {
	// Get the latest and last-processed builds for all builders.
	latest, err := getLatestBuilds()
	if err != nil {
		return nil, fmt.Errorf("Failed to get latest builds: %v", err)
	}
	lastProcessed, err := getLastProcessedBuilds()
	if err != nil {
		return nil, fmt.Errorf("Failed to get last-processed builds: %v", err)
	}
	// Find the range of uningested builds for each builder.
	type numRange struct {
		Start int // The last-ingested build number.
		End   int // The latest build number.
	}
	ranges := map[string]map[string]*numRange{}
	for _, b := range lastProcessed {
		if _, ok := ranges[b.MasterName]; !ok {
			ranges[b.MasterName] = map[string]*numRange{}
		}
		ranges[b.MasterName][b.BuilderName] = &numRange{
			Start: b.Number,
			End:   b.Number,
		}
	}
	for m, v := range latest {
		if _, ok := ranges[m]; !ok {
			ranges[m] = map[string]*numRange{}
		}
		for b, n := range v {
			if _, ok := ranges[m][b]; !ok {
				ranges[m][b] = &numRange{
					Start: -1,
					End:   n,
				}
			} else {
				ranges[m][b].End = n
			}
		}
	}
	// Create a slice of build numbers for the uningested builds.
	unprocessed := map[string]map[string][]int{}
	for m, v := range ranges {
		masterMap := map[string][]int{}
		for b, r := range v {
			builds := make([]int, r.End-r.Start)
			for i := r.Start + 1; i <= r.End; i++ {
				builds[i-r.Start-1] = i
			}
			if len(builds) > 0 {
				masterMap[b] = builds
			}
		}
		if len(masterMap) > 0 {
			unprocessed[m] = masterMap
		}
	}
	return unprocessed, nil
}

// IngestNewBuilds finds the set of uningested builds and ingests them.
func IngestNewBuilds() error {
	// TODO(borenet): Investigate the use of channels here. We should be
	// able to start ingesting builds as the data becomes available rather
	// than waiting until the end.
	buildsToProcess, err := getUningestedBuilds()
	if err != nil {
		return fmt.Errorf("Failed to obtain the set of uningested builds: %v", err)
	}
	unfinished, err := getUnfinishedBuilds()
	if err != nil {
		return fmt.Errorf("Failed to obtain the set of unfinished builds: %v", err)
	}
	for _, b := range unfinished {
		if _, ok := buildsToProcess[b.MasterName]; !ok {
			buildsToProcess[b.MasterName] = map[string][]int{}
		}
		if _, ok := buildsToProcess[b.BuilderName]; !ok {
			buildsToProcess[b.MasterName][b.BuilderName] = []int{}
		}
		buildsToProcess[b.MasterName][b.BuilderName] = append(buildsToProcess[b.MasterName][b.BuilderName], b.Number)
	}
	// TODO(borenet): Figure out how much of this is safe to parallelize.
	// We can definitely do different masters in parallel, and maybe we can
	// ingest different builders in parallel as well.
	for m, v := range buildsToProcess {
		for b, w := range v {
			for _, n := range w {
				if err := IngestBuild(m, b, n); err != nil {
					return fmt.Errorf("Failed to ingest build: %v", err)
				}
			}
		}
	}
	return nil
}