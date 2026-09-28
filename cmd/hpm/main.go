// Command hpm manages herdr plugins. Started by herdr as a plugin
// entrypoint it serves the popup; run from a shell it is a command line, and
// without arguments it opens the same manager in the terminal.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin"

	"github.com/vika2603/herdr-plugin-manager/internal/app"
	"github.com/vika2603/herdr-plugin-manager/internal/cli"
	"github.com/vika2603/herdr-plugin-manager/internal/config"
	"github.com/vika2603/herdr-plugin-manager/internal/manager"
	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/ui"
)

// Entrypoint ids declared in herdr-plugin.toml.
const (
	actionOpen  = "open"
	paneManager = "manager"
)

func main() {
	ctx, stop := plugin.ShutdownContext(context.Background())
	code := run(ctx)
	stop()
	os.Exit(code)
}

func run(ctx context.Context) int {
	// Every pane inside herdr has HERDR_ENV set, so the plugin environment is
	// recognised by an entrypoint marker, and arguments always mean the
	// command line.
	if env, err := plugin.Load(); err == nil && env.Kind() != plugin.KindUnknown && len(os.Args) == 1 {
		return newPlugin().Run(ctx)
	}
	if err := cli.Execute(ctx, app.Standalone(), runTerminal); err != nil {
		// Errors can carry the output of a plugin's build commands.
		fmt.Fprintf(os.Stderr, "%s: %s\n", app.Name, safe.Text(err.Error()))
		return 1
	}
	return 0
}

func newPlugin() *plugin.Plugin {
	p := plugin.New()
	p.Action(actionOpen, onOpen)
	p.Pane(paneManager, onManager)
	return p
}

// onOpen opens the manager popup; its placement and size come from the
// manifest.
func onOpen(ctx context.Context, env *plugin.Env) error {
	_, err := env.Client().PluginPaneOpen(ctx, herdr.PluginPaneOpenParams{
		PluginID:   env.PluginID,
		Entrypoint: paneManager,
		Focus:      herdr.Some(true),
	})
	return err
}

func onManager(ctx context.Context, env *plugin.Env) error {
	cfg, err := config.Load(env.ConfigDir)
	return ui.Run(ctx, app.ForPlugin(env), ui.Options{SelfID: env.PluginID, Keys: cfg.Keys, ConfigErr: err})
}

func runTerminal(ctx context.Context, m *manager.Manager) error {
	cfg, err := config.Load(app.ConfigDir(ctx, m))
	return ui.Run(ctx, m, ui.Options{AltScreen: true, Keys: cfg.Keys, ConfigErr: err})
}
