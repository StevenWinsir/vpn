package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// report is one usage report. Once created it is never altered: it is retried verbatim
// until the control plane acknowledges it, so a lost reply can neither double-charge
// nor lose bytes (the same discipline as the client meter).
type report struct {
	Seq   int64 `json:"seq"`
	Up    int64 `json:"up"`
	Down  int64 `json:"down"`
	Final bool  `json:"final"`
}

type grantState struct {
	// Epoch is the process generation whose counters this entry describes.
	Epoch int64 `json:"epoch"`
	Seq   int64 `json:"seq"`
	// Acked* are the cumulative counters the control plane confirmed.
	AckedUp   int64 `json:"acked_up"`
	AckedDown int64 `json:"acked_down"`
	// Cur* = Carry* + live counters at the last sample (and the checkpoint a crash
	// recovery reports from).
	CurUp     int64   `json:"cur_up"`
	CurDown   int64   `json:"cur_down"`
	CarryUp   int64   `json:"carry_up"`
	CarryDown int64   `json:"carry_down"`
	Pending   *report `json:"pending,omitempty"`
	// Closing: the next report is the final one for this epoch.
	Closing bool `json:"closing"`
	// Done: the final report was acknowledged; the id is announced as removed.
	Done bool `json:"done"`
	// Announced: the removal reached the control plane. The entry stays as a tombstone
	// for the rest of the epoch so a finished series can never be resumed.
	Announced bool `json:"announced"`
	// Dormant: not installed in the forwarder (lapsed lease or removed).
	Dormant bool `json:"dormant"`
}

type fileState struct {
	Epoch  int64                  `json:"epoch"`
	Grants map[string]*grantState `json:"grants"`
}

func loadState(path string) (fileState, error) {
	st := fileState{Grants: map[string]*grantState{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err = json.Unmarshal(data, &st); err != nil {
		return fileState{}, errors.New("agent state file is corrupt; refusing to guess (move it aside to start a new epoch)")
	}
	if st.Grants == nil {
		st.Grants = map[string]*grantState{}
	}
	return st, nil
}

// saveState writes atomically and durably: a crash leaves either the old or the new
// file, never a torn one.
func saveState(path string, st fileState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".agent-state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
