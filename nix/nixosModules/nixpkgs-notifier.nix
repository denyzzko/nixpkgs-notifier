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
        emailDefaults = lib.optionalAttrs cfg.email.enable (
          {
            EMAIL_PROVIDER = cfg.email.provider;
            SMTP_HOST = cfg.email.host;
            SMTP_PORT = toString cfg.email.port;
            SMTP_FROM = cfg.email.from;
          } // lib.optionalAttrs cfg.email.auth.enable {
            SMTP_USERNAME = cfg.email.auth.username;
            SMTP_PASSWORD = cfg.email.auth.password;
          }
        );
        sqlEsc = s: builtins.replaceStrings [ "'" ] [ "''" ] s;
        sqlEscIdent = s: builtins.replaceStrings [ "\"" ] [ "\"\"" ] s;
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
              type = types.strMatching "[a-zA-Z_][a-zA-Z0-9_]*";
              default = "nixpkgs_notifier";
              description = "Name of the PostgreSQL database to create. Must be a safe SQL identifier (letters, digits, underscores; must start with a letter or underscore).";
            };

            user = mkOption {
              type = types.strMatching "[a-zA-Z_][a-zA-Z0-9_]*";
              default = "nixpkgs_notifier";
              description = "PostgreSQL role name used by nixpkgs-notifier. Must be a safe SQL identifier (letters, digits, underscores; must start with a letter or underscore).";
            };

            password = mkOption {
              type = types.str;
              default = "";
              description = "Password for managed PostgreSQL role. Prefer setting via environmentFile in production.";
            };

            dataDir = mkOption {
              type = types.str;
              default = "/mnt/db/data/${config.services.postgresql.package.psqlSchema}";
              defaultText = literalExpression "\"/mnt/db/data/\${config.services.postgresql.package.psqlSchema} (e.g. /mnt/db/data/17)\"";
              description = "PostgreSQL data directory. Defaults to /mnt/db/data/<psqlSchema> — the same versioning NixOS uses internally — so the path is schema-versioned and survives pg_upgrade.";
              example = "/mnt/db/data/17";
            };
          };

          email = {
            enable = mkOption {
              type = types.bool;
              default = false;
              description = "Enable email notifications via SMTP.";
            };

            provider = mkOption {
              type = types.str;
              default = "smtp";
              description = "Email provider (currently only 'smtp' is supported).";
            };

            host = mkOption {
              type = types.str;
              default = "";
              description = "SMTP server hostname.";
              example = "kazi.fit.vutbr.cz";
            };

            port = mkOption {
              type = types.port;
              default = 25;
              description = "SMTP server port.";
            };

            from = mkOption {
              type = types.str;
              default = "";
              description = "Email address to use as 'From' header in notifications.";
              example = "nixpkgs-notifier@nesad.fit.vutbr.cz";
            };

            auth = {
              enable = mkOption {
                type = types.bool;
                default = false;
                description = "Enable SMTP authentication (username/password).";
              };

              username = mkOption {
                type = types.str;
                default = "";
                description = "SMTP username. Prefer setting via environmentFile in production.";
              };

              password = mkOption {
                type = types.str;
                default = "";
                description = "SMTP password. Prefer setting via environmentFile in production.";
              };
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
            services.postgresql.dataDir = cfg.database.postgresql.dataDir;
            services.postgresql.settings.port = cfg.database.postgresql.port;
            services.postgresql.ensureDatabases = [ cfg.database.postgresql.name ];
            services.postgresql.ensureUsers = [
              {
                name = cfg.database.postgresql.user;
                ensureDBOwnership = true;
              }
            ];

            systemd.services.postgresql.postStart = lib.mkAfter (lib.optionalString (cfg.database.postgresql.password != "") ''
              psql -tAc "DO \$\$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${sqlEsc cfg.database.postgresql.user}') THEN CREATE ROLE \"${sqlEscIdent cfg.database.postgresql.user}\" LOGIN; END IF; ALTER ROLE \"${sqlEscIdent cfg.database.postgresql.user}\" WITH LOGIN PASSWORD '${sqlEsc cfg.database.postgresql.password}'; END \$\$;" -d postgres
            '');
          })

          (mkIf cfg.createUser {
            users.users.${cfg.user} = {
              isSystemUser = true;
              group = cfg.group;
              home = "/var/lib/nixpkgs-notifier";
              createHome = true;
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

              environment = dbDefaults // emailDefaults // envSettings // {
                SERVER_PORT = toString cfg.port;
                HOME = "/var/lib/nixpkgs-notifier";
                XDG_CACHE_HOME = "/var/lib/nixpkgs-notifier/.cache";
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
