package billing

import (
	"errors"
	"math"
)

const ClientRatePermille int64 = 1000
const MaxClientCounter int64 = 1 << 50

type ClientCounters struct {
	Sequence      int64 `json:"sequence"`
	UploadBytes   int64 `json:"upload_bytes"`
	DownloadBytes int64 `json:"download_bytes"`
}

func (c ClientCounters) Valid() bool {
	return c.Sequence > 0 && c.Sequence <= 1<<40 && c.UploadBytes >= 0 && c.DownloadBytes >= 0 && c.UploadBytes <= MaxClientCounter && c.DownloadBytes <= MaxClientCounter
}

func ClientDelta(previous, next ClientCounters, usedUnits, upload, download int64) (up, down, units int64, err error) {
	if !next.Valid() || next.Sequence != previous.Sequence+1 || next.UploadBytes < previous.UploadBytes || next.DownloadBytes < previous.DownloadBytes {
		return 0, 0, 0, errors.New("invalid or non-monotonic client counters")
	}
	up, down = next.UploadBytes-previous.UploadBytes, next.DownloadBytes-previous.DownloadBytes
	units, err = ChargeUnits(up+down, ClientRatePermille)
	if err != nil || usedUnits < 0 || upload < 0 || download < 0 || usedUnits > math.MaxInt64-units || upload > math.MaxInt64-up || download > math.MaxInt64-down {
		return 0, 0, 0, errors.New("client accounting overflow")
	}
	return up, down, units, nil
}
