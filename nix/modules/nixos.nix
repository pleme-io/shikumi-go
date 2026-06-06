# nix/modules/nixos.nix — auto-generated typed module
# description: pleme-io's Pillar 2 — Configuration (仕組み) for Go — the counterpart to the Rust shikumi crate: the same model so every Go service and tool discovers and loads config identically.
{ config, lib, pkgs, ... }: let
  cfg = config.services.shikumi-go;
in
{
  config = lib.mkIf cfg.enable {
    environment.systemPackages = [
      cfg.package
    ];
  };
  options.services.shikumi-go = {
    enable = lib.mkEnableOption "shikumi-go";
    package = lib.mkOption {
      default = pkgs.shikumi-go or null;
      type = lib.types.package;
    };
  };
}
