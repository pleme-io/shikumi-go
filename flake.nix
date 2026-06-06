# flake.nix — auto-generated from shikumi-go.caixa.lisp
# Edit caixa source + re-render via:
#   pleme-doc-gen caixa --source shikumi-go.caixa.lisp --out . --force
# Go builders are import-paths returning whole-flake outputs
# (two-stage call at top level, NOT per-system packages).
{
  description = "shikumi-go — caixa-rendered Nix flake";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    substrate = {
      # LOCAL verification points at the feat branch carrying the Go builders.
      # The PUBLISHED repo uses: url = "github:pleme-io/substrate";
      url = "git+file:///Users/drzzln/code/github/pleme-io/substrate?ref=feat/go-pattern-parity";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = inputs @ { self, nixpkgs, substrate, ... }:
    (import substrate.goLibraryFlakeBuilder { inherit nixpkgs; }) {
      name = "shikumi-go";
      version = "0.1.0";
      src = self;
      vendorHash = null;
    };
}
