package main

import "fmt"

type serviceCommand struct {
	action string
}

func parseServiceCommand(args []string) (serviceCommand, error) {
	if len(args) == 0 {
		return serviceCommand{}, fmt.Errorf("service action is required")
	}
	command := serviceCommand{action: args[0]}
	switch command.action {
	case "install", "run", "start", "stop", "restart", "status", "uninstall":
		if len(args) != 1 {
			return serviceCommand{}, fmt.Errorf("service %s does not accept options", command.action)
		}
		return command, nil
	default:
		return serviceCommand{}, fmt.Errorf("unknown service action %q", command.action)
	}
}
