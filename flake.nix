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
      url = "github:pleme-io/substrate";
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
