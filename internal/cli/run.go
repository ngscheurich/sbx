package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/translate"
)

// runDisposable implements `sbx run [-- <argv...>]`. A disposable run is one
// `msb run` subprocess: sbx passes no --name, so msb owns the sandbox's
// naming and its whole lifecycle, and sbx adds no cleanup step. It publishes
// no ports and keeps no state to repair.
//
// Interruptions cancel ctx, which box.Run forwards to the msb subprocess.
// Whether the signal then reaches the guest itself is UNVERIFIED on a real
// host; this interim implementation must not be treated as verified signal
// forwarding.
func runDisposable(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var argv []string
	switch {
	case len(args) == 1:
		// No guest command: the configured shell runs instead.
	case args[1] == "--":
		argv = args[2:]
	default:
		fmt.Fprintf(stderr, "sbx: run takes an optional %q separator before the guest command, got %q\n", "--", strings.Join(args[1:], " "))
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	box := msb.CLI{}
	fatal := func(err error) int {
		fmt.Fprintf(stderr, "sbx: %v\n", err)
		return exitFailure
	}

	info, cfg, err := discoverConfig(ctx)
	if err != nil {
		return fatal(err)
	}

	// Secret host values travel only through the msb subprocess's
	// environment, which passes the host environment through; sbx checks
	// their presence here, before any resource changes.
	if err := translate.CheckSecretEnv(cfg, os.LookupEnv); err != nil {
		return fatal(err)
	}
	tr, err := translate.Sandbox(info, cfg, "disposable")
	if err != nil {
		return fatal(err)
	}

	if _, err := box.LocalContext(ctx); err != nil {
		return fatal(err)
	}
	// Every declared Project volume is checked against the backend's
	// existing volumes before anything is created or pulled.
	if err := checkProjectVolumes(ctx, box, tr.ProjectVolumes); err != nil {
		return fatal(err)
	}

	// A project with a [build] recipe builds its image explicitly with
	// `sbx build`; nothing else builds images, so a missing one is an
	// instruction, not a pull. Only a prebuilt image may be pulled.
	if cfg.Build != nil {
		if _, err := box.ImageInspect(ctx, cfg.Image); err != nil {
			return fatal(errImageNeedsBuild(cfg.Image))
		}
	} else if err := box.PullIfMissing(ctx, cfg.Image); err != nil {
		return fatal(err)
	}

	// The generated secret-name map holds names and "${HOST_VAR}" source
	// references only; msb resolves the values from its own environment. The
	// filesystem configuration carries the tmpfs mounts. Both are written
	// outside the repository and removed after the run: the deferred removal
	// happens after Run returns, when the subprocess no longer reads them.
	if tr.SecretConfYAML != "" {
		path, err := writeTempFile("sbx-secrets-*.yaml", tr.SecretConfYAML)
		if err != nil {
			return fatal(fmt.Errorf("writing the secret map: %w", err))
		}
		defer os.Remove(path)
		tr.Options.SecretConf = path
	}
	if tr.FsConfYAML != "" {
		path, err := writeTempFile("sbx-fs-conf-*.yaml", tr.FsConfYAML)
		if err != nil {
			return fatal(fmt.Errorf("writing the filesystem configuration: %w", err))
		}
		defer os.Remove(path)
		tr.Options.FsConf = path
	}

	guestArgv := argv
	if len(guestArgv) == 0 {
		guestArgv = []string{cfg.Shell}
	}
	code, err := box.Run(ctx, msb.RunOptions{
		CreateOptions: tr.Options,
		Workdir:       tr.Workspace,
		Argv:          guestArgv,
	}, stdin, stdout, stderr)

	// The sandbox's whole lifecycle — creation, the command, and whatever
	// happens to the sandbox afterwards — is msb run's own behavior; sbx
	// adds no cleanup step and propagates the guest's exit status.
	if err != nil && code < 0 {
		return fatal(fmt.Errorf("running %s: %w", strings.Join(guestArgv, " "), err))
	}
	return code
}
