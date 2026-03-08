# Investigation report: `11;rgb:0000/0000/0000` from Docker `ollama` command

## Scope
Investigate whether this codebase can produce/display `11;rgb:0000/0000/0000` when running `ollama` from the Docker image.

## Findings

1. **The Docker image defaults to server mode, not interactive TUI mode.**
   - `Dockerfile` sets:
     - `ENTRYPOINT ["/bin/ollama"]` (`Dockerfile:209`)
     - `CMD ["serve"]` (`Dockerfile:210`)
   - `serve` maps to `RunServer` (`cmd/cmd.go:2174-2180`) and `RunServer` only starts the API server (`cmd/cmd.go:1746-1762`).

2. **The bare `ollama` command (no subcommand) enters interactive TUI mode.**
   - Root command `Run` calls `runInteractiveTUI(cmd)` (`cmd/cmd.go:2091-2098`).
   - `runInteractiveTUI` calls `tui.Run()` (`cmd/cmd.go:1922-1924`).

3. **The TUI uses adaptive terminal colors, which triggers background detection.**
   - TUI styles use `lipgloss.AdaptiveColor` (`cmd/tui/tui.go:18-42`, `cmd/tui/tui.go:659`).
   - These styles are rendered during normal TUI drawing (`cmd/tui/tui.go:601-667`).

4. **Dependency behavior explains the exact `11;rgb:...` shape.**
   - This repo depends on:
     - `github.com/charmbracelet/lipgloss v1.1.0` (`go.mod:25`)
     - `github.com/muesli/termenv v0.16.0` (`go.mod:69`)
   - In lipgloss v1.1.0:
     - `AdaptiveColor.color()` calls `r.HasDarkBackground()`
       (`https://github.com/charmbracelet/lipgloss/blob/v1.1.0/color.go`)
     - `HasDarkBackground()` calls `r.output.HasDarkBackground()`
       (`https://github.com/charmbracelet/lipgloss/blob/v1.1.0/renderer.go`)
   - In termenv:
     - background detection sends OSC query `ESC ] 11 ; ?` via `fmt.Fprintf(tty, OSC+"%d;?"+ST, sequence)` (sequence `11`)
     - and explicitly expects responses like `"\x1b]11;rgb:1111/1111/1111\x1b\\"`
       (`https://github.com/muesli/termenv/blob/master/termenv_unix.go`).

## Conclusion

- **Yes, this codebase can display output like `11;rgb:0000/0000/0000` when the interactive TUI path is used** (for example, running bare `ollama` in an interactive Docker terminal context), because adaptive color selection relies on OSC 11 background queries through dependencies.
- **This is not expected on the default Docker startup path** (`ollama serve`), since that path does not run the TUI renderer.

## Validation limits in this environment

- Runtime reproduction was not executed here because required binaries are unavailable:
  - `docker=not-found`
  - `go=not-found`
- Findings are therefore based on static source/dependency analysis.

## Solution when when the interactive TUI path is used

This symptom is typically caused by the TUI triggering a terminal background-color query (OSC 11). In some
Docker/TTY/logging setups, the OSC response payload can end up being rendered as plain text (e.g.
`11;rgb:0000/0000/0000`).

### User/workflow workarounds (no code changes)

1. **Skip the TUI menu entirely (recommended).**
   - The TUI is entered when you run **bare** `ollama` with no subcommand.
   - Use an explicit subcommand instead, e.g.:
     - `ollama run <model>` (chat)
     - `ollama serve` (server)

2. **Disable OSC queries by making `TERM` look like a multiplexer / dumb terminal.**
   The background-color query in `termenv` is skipped when `TERM` starts with `screen`, `tmux`, or `dumb`
   (see `termenv`’s `termStatusReport` logic).

   Examples:

   ```bash
   # Run interactive TUI but prevent OSC background queries (may reduce color capability detection)
   docker run -it --rm \
     -e TERM=screen-256color \
     <your-image>
   ```

   Or if you already run inside tmux on the host, ensure the container sees a `TERM` that starts with
   `screen`/`tmux`.

3. **As a last resort: run with a truly minimal terminal.**

   ```bash
   docker run -it --rm -e TERM=dumb <your-image>
   ```

   This should suppress OSC queries, but you may lose colors and other terminal capabilities.

### Maintainer/code-level mitigation (preferred long-term)

If this output is user-visible often enough, consider preventing the adaptive color path from performing
background auto-detection:

- **Set the TUI background mode explicitly** before rendering styles so Lip Gloss does not call into
  `termenv`’s OSC 11 query.
- In Lip Gloss v1.x, this can be done via the global/default renderer:
  - `lipgloss.SetHasDarkBackground(true)` (or `false`), ideally behind an env var such as
    `OLLAMA_TUI_DARK_BG=1/0`.

This keeps the TUI fully functional while avoiding terminal background queries that can leak into output
in certain Docker/TTY environments.
