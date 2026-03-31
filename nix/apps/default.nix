{ ... }:
{
  perSystem =
    { config, pkgs, ... }:
    {
      apps.run = {
        type = "app";
        program = "${pkgs.writeShellApplication {
          name = "run-nixpkgs-notifier";
          text = ''
            set -euo pipefail
            exec ${config.packages.default}/bin/nixpkgs-notifier "$@"
          '';
        }}/bin/run-nixpkgs-notifier";
      };

      apps.default = config.apps.run;
    };
}
