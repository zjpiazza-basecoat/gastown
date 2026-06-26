// Package remote implements the client side of remote in-cluster Town access.
package remote

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/steveyegge/gastown/internal/gtcontext"
	"golang.org/x/net/websocket"
	"golang.org/x/term"
)

// AttachOptions identifies a remote tmux-backed target.
type AttachOptions struct {
	Kind string // mayor, polecat, raw target kind understood by the gateway
	Rig  string
	Name string
}

// Attach connects the local terminal to a remote Town gateway. The gateway is
// expected to attach to the tmux server running inside the target pod/sandbox.
func Attach(ctx gtcontext.Context, opts AttachOptions) error {
	if ctx.URL == "" {
		return fmt.Errorf("remote context is missing url")
	}
	endpoint, err := attachURL(ctx.URL, opts)
	if err != nil {
		return err
	}

	origin := ctx.URL
	cfg, err := websocket.NewConfig(endpoint, origin)
	if err != nil {
		return err
	}
	if ctx.Token != "" {
		cfg.Header = http.Header{"Authorization": []string{"Bearer " + ctx.Token}}
	}

	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		return fmt.Errorf("connecting remote attach websocket: %w", err)
	}
	defer ws.Close()

	return proxyTerminal(ws)
}

func attachURL(base string, opts AttachOptions) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme != "ws" && u.Scheme != "wss" {
		return "", fmt.Errorf("unsupported remote url scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/attach"
	q := u.Query()
	q.Set("kind", opts.Kind)
	if opts.Rig != "" {
		q.Set("rig", opts.Rig)
	}
	if opts.Name != "" {
		q.Set("name", opts.Name)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func proxyTerminal(ws *websocket.Conn) error {
	fd := int(os.Stdin.Fd())
	wasTerminal := term.IsTerminal(fd)
	var oldState *term.State
	var err error
	if wasTerminal {
		oldState, err = term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("setting terminal raw mode: %w", err)
		}
		defer term.Restore(fd, oldState) //nolint:errcheck
	}

	errC := make(chan error, 2)
	go func() {
		_, err := io.Copy(ws, os.Stdin)
		errC <- err
	}()
	go func() {
		_, err := io.Copy(os.Stdout, ws)
		errC <- err
	}()

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGWINCH, os.Interrupt)
	defer signal.Stop(sigC)

	for {
		select {
		case err := <-errC:
			if err == nil || err == io.EOF {
				return nil
			}
			return err
		case sig := <-sigC:
			if sig == os.Interrupt {
				return nil
			}
			// Future gateway protocol can consume resize frames. For now the next
			// attach inherits the current terminal size through tmux.
		}
	}
}
