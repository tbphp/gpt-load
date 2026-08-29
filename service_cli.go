package main

import (
	"fmt"
	"strings"
)

type serviceCommand struct {
	action    string
	configDir string
	dataDir   string
}

func parseServiceCommand(args []string) (serviceCommand, error) {
	if len(args) == 0 {
		return serviceCommand{}, fmt.Errorf("service action is required")
	}
	command := serviceCommand{action: args[0]}
	switch command.action {
	case "install", "run":
		for index := 1; index < len(args); index += 2 {
			if index+1 >= len(args) {
				return serviceCommand{}, fmt.Errorf("service option %q requires a value", args[index])
			}
			value := strings.TrimSpace(args[index+1])
			if value == "" {
				return serviceCommand{}, fmt.Errorf("service option %q must not be empty", args[index])
			}
			switch args[index] {
			case "--config-dir":
				if command.configDir != "" {
					return serviceCommand{}, fmt.Errorf("service option --config-dir is duplicated")
				}
				command.configDir = value
			case "--data-dir":
				if command.dataDir != "" {
					return serviceCommand{}, fmt.Errorf("service option --data-dir is duplicated")
				}
				command.dataDir = value
			default:
				return serviceCommand{}, fmt.Errorf("unknown service option %q", args[index])
			}
		}
		if command.action == "run" && (command.configDir == "" || command.dataDir == "") {
			return serviceCommand{}, fmt.Errorf("service run requires --config-dir and --data-dir")
		}
		return command, nil
	case "start", "stop", "restart", "status", "uninstall":
		if len(args) != 1 {
			return serviceCommand{}, fmt.Errorf("service %s does not accept options", command.action)
		}
		return command, nil
	default:
		return serviceCommand{}, fmt.Errorf("unknown service action %q", command.action)
	}
}
