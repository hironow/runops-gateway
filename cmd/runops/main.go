package main

import (
	"os"

	"github.com/hironow/runops-gateway/internal/adapter/input/cli"
	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	gcpadapter "github.com/hironow/runops-gateway/internal/adapter/output/gcp"
	slacknotifier "github.com/hironow/runops-gateway/internal/adapter/output/slack"
	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/usecase"
)

func main() {
	gcpCtrl := gcpadapter.NewController()

	notifier := slacknotifier.NewResponseURLNotifier()
	authChecker := auth.NewEnvAuthChecker()
	svc := usecase.NewRunOpsService(gcpCtrl, notifier, authChecker, state.NewMemoryStore())

	root := cli.NewRootCmd(svc)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
