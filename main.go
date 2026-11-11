// Command claudit wraps `claude -p` in a TUI: a simulated IDE replays what the agent does,
// the conversation and the prompt sit below it, and the agent's questions open popups.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/claude"
	"github.com/pixelbender/claudit/internal/history"
	"github.com/pixelbender/claudit/internal/protocol"
	"github.com/pixelbender/claudit/internal/ui"
)

const usage = `usage: claudit [claude args...]
       claudit --replay <stream.jsonl>
       claudit --version`

// version is set by release builds (make dist).
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "claudit:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// claude has no terminal of its own (see claude.start): a closed window reaches it through ctx.
	ctx, cancel := signal.NotifyContext(context.Background(), terminalSignals...)
	defer cancel()

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	var sess *claude.Session
	replay := len(args) > 0 && args[0] == "--replay"
	switch {
	case len(args) > 0 && (args[0] == "-h" || args[0] == "--help"):
		fmt.Println(usage)
		return nil
	case len(args) > 0 && args[0] == "--version":
		fmt.Println("claudit", version)
		return nil
	case replay && len(args) != 2:
		return fmt.Errorf("%s", usage)
	case replay:
		sess, err = claude.Replay(ctx, args[1])
	default: // auto mode unless the args or the settings pick another
		sess, err = claude.Spawn(ctx, claude.WithMode(args, cwd, "auto"))
	}
	if err != nil {
		return err
	}

	m := ui.New(sess.Send, cwd, replay)
	m.ContextWarn = ui.ContextWarn(os.Getenv(ui.ContextWarnEnv))
	if !replay {
		m.StartModel = claude.Model(args, cwd)
		m.UseHistory(history.File(), history.Project(cwd))
	}
	p := tea.NewProgram(m, tea.WithFilter(ui.Filter), tea.WithContext(ctx)) // a hangup ends the TUI too
	done := make(chan struct{})
	go func() {
		defer close(done)
		claude.Pump(sess.Events, func(evs []protocol.Event) {
			p.Send(ui.AgentMsg{Events: evs}) // returns at once after Run
		})
	}()
	final, err := p.Run()

	// Kill claude before exiting: the session closes Events once the process is reaped. The
	// timeout covers a grandchild that keeps claude's output pipes open.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	if m, ok := final.(*ui.Model); ok && err == nil && m.ExitReport() != "" {
		return errors.New(m.ExitReport())
	}
	return err
}
