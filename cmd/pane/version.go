package main

import "github.com/michaelquigley/push/build"

func init() {
	// pane is pre-release; advertise the dev base as v0.1.x for unstamped
	// developer builds, anticipating v0.1.0 as the first release.
	build.DevVersion = "v0.1.x"
	rootCmd.AddCommand(build.NewVersionCmd("pane"))
}
