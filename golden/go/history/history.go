package history

import (
	"time"

	"github.com/skia-dev/glog"
	"go.skia.org/infra/go/metrics"
	"go.skia.org/infra/go/tiling"
	"go.skia.org/infra/go/timer"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/golden/go/digeststore"
	"go.skia.org/infra/golden/go/expstorage"
	"go.skia.org/infra/golden/go/storage"
	"go.skia.org/infra/golden/go/types"
)

// Init initializes the history module and starts background processes to
// continuously update information about digests.
// If nTilesToBackfill is larger than zero, the given number of tiles will
// be traversed to backfill information about digests.
func Init(storages *storage.Storage, nTilesToBackfill int) error {
	var err error
	_, err = newHistorian(storages, nTilesToBackfill)
	return err
}

// CanonicalDigests returns the cannonical digests for a list of test names.
// The canonical digest is the last labeled digest in the current tile that
// is in the canonical trace. If no canonical trace is defined or the
// current tile has only untriaged digests an empty string is returned.
func CanonicalDigests(testNames []string) (map[string]string, error) {
	// TODO(stephana): Implement once the API is defined.
	return nil, nil
}

// historian runs background processes to update information about digests,
// i.e. recording when we encounter a digests for the first and the last time.
type historian struct {
	storages *storage.Storage
}

// Create a new instance of historian.
func newHistorian(storages *storage.Storage, nTilesToBackfill int) (*historian, error) {
	defer timer.New("historian").Stop()

	ret := &historian{
		storages: storages,
	}

	// Start running the background process to gather digestinfo.
	if err := ret.start(); err != nil {
		return nil, err
	}

	// There is at least one tile to backfill digestinfo then start the process.
	if nTilesToBackfill > 0 {
		ret.backFillDigestInfo(nTilesToBackfill)
	}

	return ret, nil
}

func (h *historian) start() error {
	expChanges := make(chan []string)
	h.storages.EventBus.SubscribeAsync(expstorage.EV_EXPSTORAGE_CHANGED, func(e interface{}) {
		expChanges <- e.([]string)
	})
	tileStream := h.storages.GetTileStreamNow(2*time.Minute, true)

	lastTile := <-tileStream
	if err := h.updateDigestInfo(lastTile); err != nil {
		return err
	}
	liveness := metrics.NewLiveness("digest-history-monitoring")

	// Keep processing tiles and feed them into the process channel.
	go func() {
		for {
			select {
			case tile := <-tileStream:
				if err := h.updateDigestInfo(tile); err != nil {
					glog.Errorf("Error calculating status: %s", err)
					continue
				} else {
					lastTile = tile
				}
			case <-expChanges:
				storage.DrainChangeChannel(expChanges)
				if err := h.updateDigestInfo(lastTile); err != nil {
					glog.Errorf("Error calculating tile after expectation udpate: %s", err)
					continue
				}
			}
			liveness.Update()
		}
	}()

	return nil
}

func (h *historian) updateDigestInfo(tile *tiling.Tile) error {
	return h.processTile(tile)
}

func (h *historian) backFillDigestInfo(tilesToBackfill int) {
	go func() {
		// Get the first tile and determine the tile id of the first tile
		var err error
		lastTile, err := h.storages.TileStore.Get(0, -1)
		if err != nil {
			glog.Errorf("Unable to retrieve last tile. Quiting backfill. Error: %s", err)
			return
		}

		var tile *tiling.Tile
		firstTileIndex := util.MaxInt(lastTile.TileIndex-tilesToBackfill+1, 0)
		for idx := firstTileIndex; idx <= lastTile.TileIndex; idx++ {
			if tile, err = h.storages.TileStore.Get(0, idx); err != nil {
				glog.Errorf("Unable to read tile %d from tile store. Quiting backfill. Error: %s", idx, err)
				return
			}

			// Process the tile, but giving higher priority to digests from the
			// latest tile.
			if err = h.processTile(tile); err != nil {
				glog.Errorf("Error processing tile: %s", err)
			}

			// Read the last tile, just to make sure it has not changed.
			if lastTile, err = h.storages.TileStore.Get(0, -1); err != nil {
				glog.Errorf("Unable to retrieve last tile. Quiting backfill. Error: %s", err)
				return
			}
		}
	}()
}

func (h *historian) processTile(tile *tiling.Tile) error {
	dStore := h.storages.DigestStore
	tileLen := tile.LastCommitIndex() + 1

	var digestInfo *digeststore.DigestInfo
	var ok bool
	counter := 0
	minMaxTimes := map[string]map[string]*digeststore.DigestInfo{}
	for _, trace := range tile.Traces {
		gTrace := trace.(*types.GoldenTrace)
		testName := trace.Params()[types.PRIMARY_KEY_FIELD]
		for idx, digest := range gTrace.Values[:tileLen] {
			if digest != types.MISSING_DIGEST {
				timeStamp := tile.Commits[idx].CommitTime
				if digestInfo, ok = minMaxTimes[testName][digest]; !ok {
					digestInfo = &digeststore.DigestInfo{
						TestName: testName,
						Digest:   digest,
						First:    timeStamp,
						Last:     timeStamp,
					}

					if testVal, ok := minMaxTimes[testName]; !ok {
						minMaxTimes[testName] = map[string]*digeststore.DigestInfo{digest: digestInfo}
					} else {
						testVal[digest] = digestInfo
					}
					counter++
				} else {
					digestInfo.First = util.MinInt64(digestInfo.First, timeStamp)
					digestInfo.Last = util.MaxInt64(digestInfo.Last, timeStamp)
				}
			}
		}
	}

	digestInfos := make([]*digeststore.DigestInfo, 0, counter)
	for _, digests := range minMaxTimes {
		for _, digestInfo := range digests {
			digestInfos = append(digestInfos, digestInfo)
		}
	}

	return dStore.Update(digestInfos)
}
