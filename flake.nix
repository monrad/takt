{
  description = "takt — resumable brainstorm, plan and execute for one repository";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    let
      # The version has exactly one source: the plugin manifest. `task
      # version:set VERSION=x.y.z` rewrites it, the flake reads it back, and
      # `takt version --expect-manifest` then agrees with the binary nix
      # built. Never hand-write a version string here.
      version = (builtins.fromJSON (builtins.readFile ./.claude-plugin/plugin.json)).version;

      # One package definition, used by packages.default and overlays.default.
      taktPackage =
        { lib, buildGoModule }:
        buildGoModule {
          pname = "takt";
          inherit version;
          src = ./.;
          # takt is stdlib-only: there is nothing to vendor, and the build
          # needs no network.
          vendorHash = null;
          subPackages = [ "cmd/takt" ];
          ldflags = [
            "-s"
            "-w"
            "-X github.com/monrad/takt/internal/version.Version=${version}"
          ];
          meta = {
            description = "Resumable brainstorm-plan-execute workflow with a deterministic Go core";
            homepage = "https://github.com/monrad/takt";
            license = lib.licenses.mit;
            mainProgram = "takt";
          };
        };
    in
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        packages = {
          takt = pkgs.callPackage taktPackage { };
          default = self.packages.${system}.takt;
        };

        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.golangci-lint
            pkgs.goreleaser
            pkgs.go-task
            pkgs.gh
          ];
        };

        # `nix flake check` runs the same gates as `task check`, offline.
        # golangci-lint is included because this nixpkgs pins v2.13.1, the
        # exact version .golangci.yml's golden config targets; if a nixpkgs
        # bump moves it off v2.13.x, drop it here and leave lint to CI rather
        # than lint against a config the tool no longer matches.
        checks.default =
          pkgs.runCommand "takt-check"
            {
              nativeBuildInputs = [
                pkgs.go
                pkgs.golangci-lint
                # internal/gitx and internal/wave drive a real git in a temp
                # repo (internal/testutil), so the sandbox needs the binary.
                pkgs.git
              ];
            }
            ''
              cp -R ${self} source
              chmod -R u+w source
              cd source

              export HOME="$TMPDIR"
              export GOCACHE="$TMPDIR/go-build"
              export GOMODCACHE="$TMPDIR/go-mod"
              export GOLANGCI_LINT_CACHE="$TMPDIR/golangci-lint"
              # No dependencies to fetch — fail loudly rather than reach out.
              export GOFLAGS=-mod=mod
              export GOPROXY=off

              go vet ./...
              # -race needs cgo, which the sandboxed build does not have; CI
              # runs the race detector (.github/workflows/ci.yml).
              go test ./...
              golangci-lint run ./...

              touch "$out"
            '';
      }
    )
    // {
      overlays.default = final: _prev: {
        takt = final.callPackage taktPackage { };
      };
    };
}
