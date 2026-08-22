# hoomlab — tools/ recipes
#
# Nested Go modules under tools/ — currently tools/bootstrap, the
# Proxmox + Talos bootstrap CLI (ADR-0001). Imported by the root
# justfile via `import? 'tools.just'` — variables (bin_dir, version,
# commit_hash, build_date) and settings live there. CI for these
# modules is .github/workflows/tools-ci.yml; releases are cut on demand
# via tools-release.yml.

bootstrap_dir := "tools/bootstrap"
bootstrap_bin := bin_dir / "bootstrap"

# ─── Bootstrap CLI ──────────────────────────────────────────────────

# Build the bootstrap CLI into build/bin/bootstrap
[group('tools')]
bootstrap-build:
    @mkdir -p {{ bin_dir }}
    @cd {{ bootstrap_dir }} && go build \
        -ldflags "-X main.version={{ version }} -X main.commit={{ commit_hash }} -X main.date={{ build_date }}" \
        -o "$OLDPWD/{{ bootstrap_bin }}" .
    @echo "✓ bootstrap built → {{ bootstrap_bin }}"

# Run bootstrap module tests with the race detector
[group('tools')]
bootstrap-test:
    @cd {{ bootstrap_dir }} && go test -v -race ./...

# Lint the bootstrap module (root .golangci.yml applies)
[group('tools')]
bootstrap-lint:
    @cd {{ bootstrap_dir }} && golangci-lint run ./...

# Build then run the bootstrap CLI: just bootstrap-run -- --help
[group('tools')]
bootstrap-run *args: bootstrap-build
    @{{ bootstrap_bin }} {{ args }}
