package status

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rcrowley/go-metrics"
	"github.com/skia-dev/glog"
	imetrics "go.skia.org/infra/go/metrics"
	"go.skia.org/infra/go/tiling"
	"go.skia.org/infra/go/timer"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/golden/go/expstorage"
	"go.skia.org/infra/golden/go/storage"
	"go.skia.org/infra/golden/go/types"
)

const (
	// Metric names and templates for metric names added in this file.
	METRIC_TOTAL       = "status.digests.total"
	METRIC_ALL_TMPL    = "status.%s.all"
	METRIC_CORPUS_TMPL = "status.%s.by_corpus.%s"
)

var (
	// Gauges to track overall digests with different labels.
	allUntriagedGauge = metrics.NewRegisteredGauge(fmt.Sprintf(METRIC_ALL_TMPL, types.UNTRIAGED), nil)
	allPositiveGauge  = metrics.NewRegisteredGauge(fmt.Sprintf(METRIC_ALL_TMPL, types.POSITIVE), nil)
	allNegativeGauge  = metrics.NewRegisteredGauge(fmt.Sprintf(METRIC_ALL_TMPL, types.NEGATIVE), nil)
	totalGauge        = metrics.NewRegisteredGauge(METRIC_TOTAL, nil)

	// Gauges to track counts of digests by corpus / label
	corpusGauges = map[string]map[types.Label]metrics.Gauge{}
)

// GUIStatus reflects the current rebaseline status. In particular whether
// HEAD is baselined and how many untriaged and negative digests there
// currently are.
type GUIStatus struct {
	// Indicates whether current HEAD is ok.
	OK bool `json:"ok"`

	// Last commit currently know.
	LastCommit *tiling.Commit `json:"lastCommit"`

	// Status per corpus.
	CorpStatus []*GUICorpusStatus `json:"corpStatus"`
}

type GUICorpusStatus struct {
	// Name of the corpus.
	Name string `json:"name"`

	// Indicats whether this status is ok.
	OK bool `json:"ok"`

	// Earliest commit hash considered HEAD (is not always the last commit).
	MinCommitHash string `json:"minCommitHash"`

	// Number of untriaged digests in HEAD.
	UntriagedCount int `json:"untriagedCount"`

	// Number of negative digests in HEAD.
	NegativeCount int `json:"negativeCount"`
}

type CorpusStatusSorter []*GUICorpusStatus

// Implement sort.Interface
func (c CorpusStatusSorter) Len() int           { return len(c) }
func (c CorpusStatusSorter) Less(i, j int) bool { return c[i].Name < c[j].Name }
func (c CorpusStatusSorter) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }

type StatusWatcher struct {
	storages *storage.Storage

	current *GUIStatus
	mutex   sync.Mutex
}

func New(storages *storage.Storage) (*StatusWatcher, error) {
	ret := &StatusWatcher{
		storages: storages,
	}

	if err := ret.calcAndWatchStatus(); err != nil {
		return nil, err
	}

	return ret, nil
}

func (s *StatusWatcher) GetStatus() *GUIStatus {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.current
}

func (s *StatusWatcher) calcAndWatchStatus() error {
	expChanges := make(chan []string)
	s.storages.EventBus.SubscribeAsync(expstorage.EV_EXPSTORAGE_CHANGED, func(e interface{}) {
		expChanges <- e.([]string)
	})

	tileStream := s.storages.GetTileStreamNow(2*time.Minute, false)

	lastTile := <-tileStream
	if err := s.calcStatus(lastTile); err != nil {
		return err
	}

	liveness := imetrics.NewLiveness("status-monitoring")

	go func() {
		for {
			select {
			case <-tileStream:
				tile, err := s.storages.GetLastTileTrimmed(false)
				if err != nil {
					glog.Errorf("Error retrieving tile: %s", err)
					continue
				}
				if err := s.calcStatus(tile); err != nil {
					glog.Errorf("Error calculating status: %s", err)
				} else {
					lastTile = tile
					liveness.Update()
				}
			case <-expChanges:
				storage.DrainChangeChannel(expChanges)
				if err := s.calcStatus(lastTile); err != nil {
					glog.Errorf("Error calculating tile after expectation update: %s", err)
				}
				liveness.Update()
			}
		}
	}()

	return nil
}

func (s *StatusWatcher) calcStatus(tile *tiling.Tile) error {
	defer timer.New("Calc status timer:").Stop()

	minCommitId := map[string]int{}
	okByCorpus := map[string]bool{}

	expectations, err := s.storages.ExpectationsStore.Get()
	if err != nil {
		return err
	}

	// Gathers unique labels by corpus and label.
	byCorpus := map[string]map[types.Label]map[string]bool{}

	// Iterate over the current traces
	tileLen := tile.LastCommitIndex() + 1
	for _, trace := range tile.Traces {
		gTrace := trace.(*types.GoldenTrace)

		idx := tileLen - 1
		for (idx >= 0) && (gTrace.Values[idx] == types.MISSING_DIGEST) {
			idx--
		}

		// If this is an empty trace we ignore it for now.
		if idx == -1 {
			continue
		}

		// If this corpus doesn't exist yet, we initialize it.
		corpus := gTrace.Params()[types.CORPUS_FIELD]
		if _, ok := byCorpus[corpus]; !ok {
			minCommitId[corpus] = tileLen
			okByCorpus[corpus] = true
			byCorpus[corpus] = map[types.Label]map[string]bool{
				types.POSITIVE:  map[string]bool{},
				types.NEGATIVE:  map[string]bool{},
				types.UNTRIAGED: map[string]bool{},
			}

			if _, ok := corpusGauges[corpus]; !ok {
				corpusGauges[corpus] = map[types.Label]metrics.Gauge{
					types.UNTRIAGED: metrics.NewRegisteredGauge(fmt.Sprintf(METRIC_CORPUS_TMPL, types.UNTRIAGED, corpus), nil),
					types.POSITIVE:  metrics.NewRegisteredGauge(fmt.Sprintf(METRIC_CORPUS_TMPL, types.POSITIVE, corpus), nil),
					types.NEGATIVE:  metrics.NewRegisteredGauge(fmt.Sprintf(METRIC_CORPUS_TMPL, types.NEGATIVE, corpus), nil),
				}
			}
		}

		// Account for the corpus and testname.
		digest := gTrace.Values[idx]
		testName := gTrace.Params()[types.PRIMARY_KEY_FIELD]
		status := expectations.Classification(testName, digest)

		digestInfo, err := s.storages.GetOrUpdateDigestInfo(testName, digest, tile.Commits[idx])
		if err != nil {
			return err
		}

		okByCorpus[corpus] = okByCorpus[corpus] && ((status == types.POSITIVE) ||
			((status == types.NEGATIVE) && (len(digestInfo.IssueIDs) > 0)))
		minCommitId[corpus] = util.MinInt(idx, minCommitId[corpus])
		byCorpus[corpus][status][digest] = true
	}

	commits := tile.Commits[:tileLen]
	overallOk := true
	allUntriagedCount := 0
	allPositiveCount := 0
	allNegativeCount := 0
	corpStatus := make([]*GUICorpusStatus, 0, len(byCorpus))
	for corpus := range byCorpus {
		overallOk = overallOk && okByCorpus[corpus]
		untriagedCount := len(byCorpus[corpus][types.UNTRIAGED])
		positiveCount := len(byCorpus[corpus][types.POSITIVE])
		negativeCount := len(byCorpus[corpus][types.NEGATIVE])
		corpStatus = append(corpStatus, &GUICorpusStatus{
			Name:           corpus,
			OK:             okByCorpus[corpus],
			MinCommitHash:  commits[minCommitId[corpus]].Hash,
			UntriagedCount: untriagedCount,
			NegativeCount:  negativeCount,
		})
		allUntriagedCount += untriagedCount
		allNegativeCount += negativeCount
		allPositiveCount += positiveCount

		corpusGauges[corpus][types.POSITIVE].Update(int64(positiveCount))
		corpusGauges[corpus][types.NEGATIVE].Update(int64(negativeCount))
		corpusGauges[corpus][types.UNTRIAGED].Update(int64(untriagedCount))
	}
	allUntriagedGauge.Update(int64(allUntriagedCount))
	allPositiveGauge.Update(int64(allPositiveCount))
	allNegativeGauge.Update(int64(allNegativeCount))
	totalGauge.Update(int64(allUntriagedCount + allPositiveCount + allNegativeCount))

	sort.Sort(CorpusStatusSorter(corpStatus))

	// Swap out the current tile.
	result := &GUIStatus{
		OK:         overallOk,
		LastCommit: commits[tileLen-1],
		CorpStatus: corpStatus,
	}
	s.mutex.Lock()
	s.current = result
	s.mutex.Unlock()

	return nil
}
