# man/ — Unix manual page

`sharedwatch.1` is the canonical `man sharedwatch` page. Section 1 (user commands).

## Installing it

### System-wide (preferred when you have root)

```bash
sudo cp man/sharedwatch.1 /usr/local/share/man/man1/
sudo mandb                       # rebuild man index (optional; most modern systems auto-refresh)
man sharedwatch                  # verify
```

### Per-user (no sudo)

```bash
mkdir -p ~/.local/share/man/man1
cp man/sharedwatch.1 ~/.local/share/man/man1/

# Add to your shell rc once if it isn't already:
export MANPATH="$HOME/.local/share/man:$MANPATH"

man sharedwatch                  # verify
```

If `man sharedwatch` still says "No manual entry" after a per-user install, your `man` doesn't include `$HOME/.local/share/man` in its search path. Confirm with `manpath` and either fix `MANPATH` or symlink the file into a directory `manpath` already lists.

### Quick preview without installing

```bash
man -l man/sharedwatch.1
# or, plain text:
man --warnings -P cat -l man/sharedwatch.1
```

## Keeping it in sync

The man page is hand-authored nroff, not generated. When commands or flags change in `src/cmd/sharedwatch/main.go`, update `man/sharedwatch.1` in the same commit. The `.TH` header carries the version string — bump it alongside `src/CHANGELOG.md`.
