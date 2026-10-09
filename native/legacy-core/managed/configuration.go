package managed

import "context"

type ConfigurationOwner struct {
	Generation uint64 `json:"generation"`
	UserID     string `json:"user_id"`
	SessionID  string `json:"session_id"`
}

type ConfigurationGroup struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Selected string   `json:"selected"`
	Proxies  []string `json:"proxies"`
}

type ConfigurationView struct {
	ID      string               `json:"id"`
	Version string               `json:"version"`
	Owner   ConfigurationOwner   `json:"owner"`
	Groups  []ConfigurationGroup `json:"groups"`
}

type PreparedConfiguration interface {
	Apply(context.Context, ConfigurationOwner) (ConfigurationView, error)
}

type ConfigurationEngine interface {
	Prepare(context.Context, Profile) (PreparedConfiguration, error)
	Clear() error
	Select(context.Context, ConfigurationOwner, string, string, string) ([]ConfigurationGroup, error)
}

func CopyConfiguration(value *ConfigurationView) *ConfigurationView {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Groups = make([]ConfigurationGroup, len(value.Groups))
	for i, group := range value.Groups {
		copy.Groups[i] = group
		copy.Groups[i].Proxies = append([]string(nil), group.Proxies...)
	}
	return &copy
}

func (c *Coordinator) configurationOwnerLocked() ConfigurationOwner {
	return ConfigurationOwner{c.snapshot.Generation, c.snapshot.User.ID, c.snapshot.Session.SessionID}
}

func (c *Coordinator) discardConfigurationLocked() error {
	c.profile = nil
	c.snapshot.Configuration = nil
	c.snapshot.ProfileVersion = ""
	c.snapshot.CanConnect = false
	c.runtime.Stop()
	if c.configuration != nil {
		return c.configuration.Clear()
	}
	return nil
}

func (c *Coordinator) SelectProxy(ctx context.Context, generation uint64, configurationID, group, proxy string) (AccountSnapshot, error) {
	c.mu.Lock()
	if c.profile != nil && len(c.profile.Nodes) != 0 {
		c.mu.Unlock()
		return c.request(ctx, generation, true, &nodeSelection{configurationID, group, proxy})
	}
	defer c.mu.Unlock()
	c.snapshotLocked()
	if c.snapshot.Generation != generation {
		return c.snapshotLocked(), &APIError{"operation_superseded"}
	}
	view := c.snapshot.Configuration
	if c.snapshot.Busy || view == nil || c.configuration == nil || view.ID != configurationID {
		return c.snapshotLocked(), &APIError{"managed_configuration_required"}
	}
	groups, err := c.configuration.Select(ctx, c.configurationOwnerLocked(), configurationID, group, proxy)
	if err != nil {
		return c.snapshotLocked(), err
	}
	view.Groups = groups
	return c.snapshotLocked(), nil
}

type nodeSelection struct {
	configurationID string
	group           string
	proxy           string
}
