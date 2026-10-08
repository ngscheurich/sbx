// Image checks: a project's declared image_check script gates image use.
// The script runs in an isolated check sandbox created from the image
// alone — no Workspace, volumes, secrets, or project network policy — and
// the check sandbox is removed on every exit path, cancellation included
// (ADR-0004). Successes are recorded in host state keyed by the image's
// manifest digest and the script's content hash; a changed script needs a
// new pass but is not Creation drift. A failed check never touches an
// existing sandbox or its data.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/ngscheurich/sbx/internal/config"
	"github.com/ngscheurich/sbx/internal/msb"
	"github.com/ngscheurich/sbx/internal/state"
	"github.com/ngscheurich/sbx/internal/translate"
)

// imageCheckGuestPath is the guest path the check script is copied to
// inside the check sandbox, and the token the fake msb keys its check
// behavior on in tests.
const imageCheckGuestPath = "/tmp/sbx-image-check"

// imageCheckScript resolves the declared image_check path against the
// worktree root with sbx's host-path rules and returns the script's
// contents. A missing or directory path is an error before anything is
// created.
func imageCheckScript(worktreeRoot, declared string) (string, error) {
	resolved, isDir, err := translate.ResolveHostPath(worktreeRoot, declared, "image check")
	if err != nil {
		return "", err
	}
	if isDir {
		return "", fmt.Errorf("image check %q is a directory (resolved to %s); name the project-owned guest script", declared, resolved)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("reading the image check script %s: %w", resolved, err)
	}
	return string(data), nil
}

// recordedPass reports whether a success is recorded for the image digest
// and the script contents. A corrupt record is an error, never a pass.
func recordedPass(digest, script string) (bool, error) {
	return state.HasImageCheckSuccess(digest, state.ImageCheckScriptHash(script))
}

// runCheckSandbox runs the image check in an isolated sandbox: created
// from the image alone — no Workspace, volumes, secrets, or project
// network policy, carrying only the project's resource limits — labeled
// as sbx's own, and removed on every exit path, guest failure,
// interruption, and success alike, with a context that survives the
// caller's cancellation. On a guest success it first confirms the image
// still resolves to the digest the check started from, and only then
// records the pass in host state: a tag repointed mid-check records
// nothing, because the pass would attest contents the script never ran
// against.
func runCheckSandbox(ctx context.Context, box msb.CLI, cfg config.Config, worktreeRoot, baseName, script, digest string, out *output) error {
	suffix, err := randomHex(6)
	if err != nil {
		return fmt.Errorf("naming the image-check sandbox: %w", err)
	}
	name := baseName + "-imgchk-" + suffix
	if err := box.Create(ctx, msb.CreateOptions{
		Name:   name,
		Image:  cfg.Image,
		CPUs:   cfg.CPUs,
		Memory: cfg.Memory,
		Labels: []msb.Label{
			{Key: "sbx.managed", Value: "1"},
			{Key: "sbx.mode", Value: "image-check"},
			{Key: "sbx.worktree", Value: worktreeRoot},
		},
	}); err != nil {
		return fmt.Errorf("creating the image-check sandbox: %w", err)
	}
	// Removal survives cancellation: an interrupted check still leaves no
	// sandbox behind.
	removeCtx := context.WithoutCancel(ctx)
	defer func() {
		if err := box.Remove(removeCtx, name); err != nil {
			fmt.Fprintf(out.stderr, "%s removing the image-check sandbox %s failed: %v\n", out.styles.Warning.Render("warning:"), name, err)
		}
	}()

	// The script travels by content through the exec's standard input,
	// never as a host mount: the check sandbox has no Workspace, volumes,
	// secrets, or project network policy.
	argv := []string{"/bin/sh", "-c",
		"cat > " + imageCheckGuestPath + " && chmod +x " + imageCheckGuestPath + " && exec " + imageCheckGuestPath}
	code, err := box.Exec(ctx, name, "", argv, strings.NewReader(script), out.rawOut, out.rawErr)
	if err != nil && code < 0 {
		return fmt.Errorf("the image check was interrupted before it finished: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("the image check failed with exit status %d; the image may not be used until the check passes for its contents", code)
	}
	// The check ran against whatever the image reference resolves to now;
	// a success is recorded only when that is still the digest the check
	// was started for.
	if info, err := box.ImageInspect(ctx, cfg.Image); err != nil {
		return fmt.Errorf("confirming %s’s contents after the image check: %w", cfg.Image, err)
	} else if info.ManifestDigest != digest {
		return fmt.Errorf("image %s changed while its image check ran (%s became %s); nothing was recorded — run the command again to check the new contents", cfg.Image, digest, info.ManifestDigest)
	}
	if err := state.SaveImageCheckSuccess(digest, state.ImageCheckScriptHash(script)); err != nil {
		return fmt.Errorf("the image check passed, but recording its success failed: %w", err)
	}
	fmt.Fprintf(out.stderr, "sbx: image check passed for %s (image contents %s)\n", cfg.Image, digest)
	return nil
}

// ensureImageChecked applies the image-check gate to cfg.Image's current
// contents, resolving the digest itself: a recorded pass for these
// contents and the current script is reused, otherwise the check runs.
// Before returning, it confirms the image still resolves to the contents
// the check covered, so a sandbox is never created from contents that
// changed underneath a just-finished check.
func ensureImageChecked(ctx context.Context, box msb.CLI, cfg config.Config, worktreeRoot, baseName string, out *output) error {
	if cfg.ImageCheck == "" {
		return nil
	}
	script, err := imageCheckScript(worktreeRoot, cfg.ImageCheck)
	if err != nil {
		return err
	}
	info, err := box.ImageInspect(ctx, cfg.Image)
	if err != nil {
		return fmt.Errorf("the image check needs %s’s contents, but msb cannot inspect the image: %w", cfg.Image, err)
	}
	digest := info.ManifestDigest
	ok, err := recordedPass(digest, script)
	if err != nil {
		return err
	}
	if !ok {
		if err := runCheckSandbox(ctx, box, cfg, worktreeRoot, baseName, script, digest, out); err != nil {
			return err
		}
	}
	info, err = box.ImageInspect(ctx, cfg.Image)
	if err != nil {
		return fmt.Errorf("confirming %s’s contents after the image check: %w", cfg.Image, err)
	}
	if info.ManifestDigest != digest {
		return fmt.Errorf("image %s changed while its image check ran (%s became %s); nothing was created — run the command again to check the new contents", cfg.Image, digest, info.ManifestDigest)
	}
	return nil
}

// ensureSandboxImageChecked applies the gate to an existing persistent
// sandbox's image — even with --allow-stale, and even when the sandbox is
// already running. The gate is evaluated against the sandbox's own image
// contents; the image reference must still resolve to them for a check to
// run, so a tag repointed away from the sandbox's contents fails closed
// rather than guess. A failed check never touches the sandbox or its data.
func ensureSandboxImageChecked(ctx context.Context, box msb.CLI, p *persistent, sandboxDigest string, out *output) error {
	if p.cfg.ImageCheck == "" {
		return nil
	}
	if sandboxDigest == "" {
		return fmt.Errorf("the persistent sandbox %s does not report its image contents, so sbx cannot verify its image check; sbx refuses to use it", p.id.Sandbox)
	}
	script, err := imageCheckScript(p.info.WorktreeRoot, p.cfg.ImageCheck)
	if err != nil {
		return err
	}
	ok, err := recordedPass(sandboxDigest, script)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	info, err := box.ImageInspect(ctx, p.cfg.Image)
	if err != nil {
		return fmt.Errorf("the persistent sandbox %s was created from image contents %s, which have no recorded image-check pass for the current script, and msb cannot inspect %s to re-check them: %w", p.id.Sandbox, sandboxDigest, p.cfg.Image, err)
	}
	if info.ManifestDigest != sandboxDigest {
		return fmt.Errorf("the persistent sandbox %s was created from image contents %s, which have no recorded image-check pass for the current script, and %s now resolves to %s; sbx cannot run the check against the sandbox’s contents, so it refuses to use the sandbox", p.id.Sandbox, sandboxDigest, p.cfg.Image, info.ManifestDigest)
	}
	return runCheckSandbox(ctx, box, p.cfg, p.info.WorktreeRoot, p.id.Sandbox, script, sandboxDigest, out)
}

// randomHex returns n hex characters of cryptographic randomness, for
// backend-generated sandbox names that never collide across processes.
func randomHex(n int) (string, error) {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b)[:n], nil
}
