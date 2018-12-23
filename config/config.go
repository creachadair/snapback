// Package config describes configuration settings for the snapback tool.
package config

import (
	"io"

	"bitbucket.org/creachadair/tarsnap"
	yaml "gopkg.in/yaml.v2"
)

// A Config contains settings for the snapback tool.
type Config struct {
	// An ordered list of backups to be created.
	Backup []Backup `yaml:"backup"`

	// Working directory from which backups should be run.  This can be
	// overridden by individual backups. If this is not specified, the directory
	// containing the configuration file is used.
	WorkDir string `yaml:"workdir"`
}

// TODO: Add pruning policy.

// A Backup describes a collection of files to be backed up as a unit together.
type Backup struct {
	// The name defines the base name of the archive. A timestamp will be
	// appended to this name to obtain the complete name.
Name string `yaml:"name"`

	// The archive creation options for this backup.
	tarsnap.CreateOptions `yaml:",inline"`
}

// Parse decodes a *Config from the specified reader.
func Parse(r io.Reader) (*Config, error) {
	dec := yaml.NewDecoder(r)
	dec.SetStrict(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
