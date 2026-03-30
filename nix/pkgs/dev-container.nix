{ inputs, lib, ... }:
{
  perSystem =
    { config, pkgs, system, ... }:
    let
      mkDevContainer =
        targetSystem:
        let
          testNixos = inputs.nixpkgs.lib.nixosSystem {
            system = targetSystem;
            modules = [
              inputs.self.nixosModules.nixpkgs-notifier
              ({ pkgs, ... }: {
                boot.isContainer = true;

                networking = {
                  firewall.enable = false;
                  hostName = "nixpkgs-notifier-test";
                  useDHCP = false;
                  interfaces = { };
                  nameservers = [ "1.1.1.1" "8.8.8.8" ];
                };

                users.users.nixos = {
                  isNormalUser = true;
                  initialPassword = "nixos";
                  extraGroups = [ "wheel" ];
                  shell = pkgs.bashInteractive;
                };

                services.openssh = {
                  enable = true;
                  settings.PasswordAuthentication = true;
                  ports = [ 3333 ];
                };

                environment.systemPackages = with pkgs; [
                  bash
                  coreutils
                  curl
                  iproute2
                  inetutils
                  systemd
                ];

                # Enable nixpkgs-notifier with dummy credentials so systemd
                # generates and enables the unit correctly. The service will
                # fail at runtime (no real DB / OIDC), which is expected —
                # the test only validates the module structure.
                services.nixpkgs-notifier = {
                  enable = true;
                  package = config.packages.nixpkgs-notifier;
                  port = 8080;
                  settings = {
                    SERVER_URL = "http://localhost:8080";
                    DB_HOST = "127.0.0.1";
                    DB_PORT = "5432";
                    DB_NAME = "nixpkgs_notifier";
                    DB_USER = "nixpkgs_notifier";
                    DB_PASS = "test";
                    DB_SSLMODE = "disable";
                    OIDC_PROVIDERS = "[{\"name\":\"test\",\"issuer\":\"https://example.com\",\"client_id\":\"test\",\"client_secret\":\"test\"}]";
                    EMAIL_PROVIDER = "smtp";
                    SMTP_HOST = "localhost";
                    SMTP_PORT = "25";
                    SMTP_FROM = "noreply@example.com";
                  };
                };

                system.stateVersion = "25.11";
              })
            ];
          };

          initFix = pkgs.runCommand "add-init" { preferLocalBuild = true; } ''
            mkdir -p $out
            ln -s ${pkgs.systemd}/lib/systemd/systemd $out/init
          '';
        in
        pkgs.dockerTools.buildImage {
          name = "dev-container";
          tag = "latest";

          config = {
            Cmd = [ "/init" ];
            StopSignal = "SIGRTMIN+3";
            Hostname = "nixpkgs-notifier-test";
            ExposedPorts = {
              "3333/tcp" = { };
              "8080/tcp" = { };
            };
          };

          copyToRoot = pkgs.symlinkJoin {
            name = "dev-container-root";
            paths = [
              testNixos.config.system.build.toplevel
              initFix
            ];
          };
        };
    in
    lib.optionalAttrs (lib.elem system [ "x86_64-linux" "aarch64-linux" ]) {
      packages.dev-container = mkDevContainer system;
    };
}
