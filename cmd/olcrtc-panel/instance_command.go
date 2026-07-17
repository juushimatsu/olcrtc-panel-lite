package main

import (
	"errors"
	"flag"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/instance"
)

func instanceCommand(args []string) error {
	if len(args) == 0 || args[0] != "prepare" {
		return errors.New("instance action must be prepare")
	}
	flags := flag.NewFlagSet("instance prepare", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/olcrtc-panel/config.yaml", "panel YAML config")
	id := flags.Int64("id", 0, "instance ID")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	return instance.PreparePermissions(cfg, *id)
}
