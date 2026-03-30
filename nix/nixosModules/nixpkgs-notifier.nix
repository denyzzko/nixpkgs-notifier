{ moduleWithSystem, ... }:
{
  flake.nixosModules.nixpkgs-notifier = moduleWithSystem (
    perSystem @ { ... }: nixos @ { config, lib, pkgs, ... }:
      let
        cfg = config.services.nixpkgs-notifier;
        envSettings = lib.mapAttrs (_: value: toString value) cfg.settings;
        dbDefaults = lib.optionalAttrs cfg.database.postgresql.enable {
          DB_HOST = "127.0.0.1";
          DB_PORT = toString cfg.database.postgresql.port;
          DB_NAME = cfg.database.postgresql.name;
          DB_USER = cfg.database.postgresql.user;
          DB_PASS = cfg.database.postgresql.password;
          DB_SSLMODE = "disable";
        };
        sqlEsc = s: builtins.replaceStrings [ "'" ] [ "''" ] s;
      in
      with lib;
      {
        options.services.nixpkgs-notifier = {
          enable = mkEnableOption "nixpkgs-notifier server";

          package = mkOption {
            type = types.package;
            default = config.packages.nixpkgs-notifier;
            defaultText = literalExpression "config.packages.nixpkgs-notifier";
            description = "Package providing the nixpkgs-notifier server binary.";
          };

          port = mkOption {
            type = types.port;
            default = 8080;
            description = "Port passed to the service as SERVER_PORT.";
          };

          openFirewall = mkOption {
            type = types.bool;
            default = false;
            description = "Open configured port in the firewall.";
          };

          settings = mkOption {
            type = types.attrsOf (types.oneOf [ types.str types.int types.bool ]);
            default = { };
            description = "Additional environment variables for nixpkgs-notifier.";
            example = literalExpression ''
              {
                SERVER_URL = "https://notifier.example.com";
                TLS_MODE = "off";
                DB_HOST = "127.0.0.1";
                DB_PORT = "5432";
                DB_NAME = "nixpkgs_notifier";
                DB_USER = "nixpkgs_notifier";
                DB_PASS = "secret";
                DB_SSLMODE = "disable";
                OIDC_PROVIDERS = "[{\"name\":\"authentik\",\"display_name\":\"School SSO\",\"issuer\":\"https://auth.example.com/application/o/notifier/\",\"client_id\":\"example-client-id\",\"client_secret\":\"example-client-secret\"}]";
                EMAIL_PROVIDER = "smtp";
                SMTP_HOST = "localhost";
                SMTP_PORT = "25";
                SMTP_FROM = "noreply@example.com";
              }
            '';
          };

          database.postgresql = {
            enable = mkOption {
              type = types.bool;
              default = false;
              description = "Enable local PostgreSQL service and provision DB/user for nixpkgs-notifier.";
            };

            port = mkOption {
              type = types.port;
              default = 5432;
              description = "PostgreSQL port for local managed database.";
            };

            name = mkOption {
              type = types.str;
              default = "nixpkgs_notifier";
              description = "Name of the PostgreSQL database to create.";
            };

            user = mkOption {
              type = types.str;
              default = "nixpkgs_notifier";
              description = "PostgreSQL role name used by nixpkgs-notifier.";
            };

            password = mkOption {
              type = types.str;
              default = "";
              description = "Password for managed PostgreSQL role. Prefer setting via environmentFile in production.";
            };
          };

          environmentFile = mkOption {
            type = types.nullOr types.str;
            default = null;
            description = "Optional systemd EnvironmentFile path with secrets and/or overrides.";
          };

          user = mkOption {
            type = types.str;
            default = "nixpkgs-notifier";
            description = "User running nixpkgs-notifier service.";
          };

          group = mkOption {
            type = types.str;
            default = "nixpkgs-notifier";
            description = "Group running nixpkgs-notifier service.";
          };

          createUser = mkOption {
            type = types.bool;
            default = true;
            description = "Create system user/group automatically.";
          };
        };

        config = mkIf cfg.enable (mkMerge [
          (mkIf cfg.database.postgresql.enable {
            services.postgresql.enable = true;
            services.postgresql.settings.port = cfg.database.postgresql.port;
            services.postgresql.ensureDatabases = [ cfg.database.postgresql.name ];
            services.postgresql.ensureUsers = [
              {
                name = cfg.database.postgresql.user;
                ensureDBOwnership = true;
              }
            ];

            systemd.services.postgresql.postStart = lib.mkAfter (lib.optionalString (cfg.database.postgresql.password != "") ''
              psql -tAc "ALTER ROLE \"${cfg.database.postgresql.user}\" WITH LOGIN PASSWORD '${sqlEsc cfg.database.postgresql.password}'" -d postgres
            '');
          })

          (mkIf cfg.createUser {
            users.users.${cfg.user} = {
              isSystemUser = true;
              group = cfg.group;
            };

            users.groups.${cfg.group} = { };
          })

          {
            systemd.services.nixpkgs-notifier = {
              description = "nixpkgs-notifier server";
              after = [ "network-online.target" ] ++ optionals cfg.database.postgresql.enable [ "postgresql.service" ];
              wants = [ "network-online.target" ] ++ optionals cfg.database.postgresql.enable [ "postgresql.service" ];
              wantedBy = [ "multi-user.target" ];

              path = [ pkgs.nix ];

              environment = dbDefaults // envSettings // {
                SERVER_PORT = toString cfg.port;
              };

              serviceConfig = {
                ExecStart = "${cfg.package}/bin/nixpkgs-notifier";
                User = cfg.user;
                Group = cfg.group;
                Restart = "on-failure";
                RestartSec = "10s";
                StateDirectory = "nixpkgs-notifier";
                WorkingDirectory = "/var/lib/nixpkgs-notifier";
              } // optionalAttrs (cfg.environmentFile != null) {
                EnvironmentFile = cfg.environmentFile;
              };
            };
          }

          (mkIf cfg.openFirewall {
            networking.firewall.allowedTCPPorts = [ cfg.port ];
          })
        ]);
      }
  );
}