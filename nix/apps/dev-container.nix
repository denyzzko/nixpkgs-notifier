{ ... }:
{
  perSystem = { pkgs, ... }: {
    apps."dev-container" = {
      type = "app";
      program = "${pkgs.writeShellApplication {
        name = "dev-container";
        runtimeInputs = [
          pkgs.nix
          pkgs.docker
          pkgs.openssh
          pkgs.coreutils
          pkgs.gnugrep
          pkgs.git
        ];
        text = ''
          set -euo pipefail

          find_repo_root() {
            local dir="$PWD"

            while [ "$dir" != "/" ]; do
              if [ -f "$dir/flake.nix" ]; then
                echo "$dir"
                return 0
              fi

              dir="$(dirname "$dir")"
            done

            echo "Run from the repository root or one of its subdirectories so flake.nix can be found." >&2
            exit 1
          }

          image_name="nixpkgs-notifier-dev-container"
          container_name="nixpkgs-notifier-dev-container-instance"
          version_file="''${XDG_CACHE_HOME:-$HOME/.cache}/nixpkgs-notifier-dev-container-version"
          target_system="''${TARGET_SYSTEM:-x86_64-linux}"
          package_attr="dev-container"
          repo_root="$(find_repo_root)"
          flake_ref="git+file://$repo_root"
          oidc_env_file="$repo_root/.env.oidc.local"
          container_oidc_env_file="/etc/nixpkgs-notifier-oidc.env"
          container_state_dir="/mnt/db"
          state_volume="''${container_name}-state"

          mkdir -p "$(dirname "$version_file")"

          usage() {
            cat <<EOF
          Usage: dev-container <command> [options]

          Commands:
            up      Build and start the dev container (detached)
            exec    Execute a command in the running container (default: bash)
            down    Stop and remove the dev container
            ps      List dev containers
            status  Show detailed container status

          Examples:
            dev-container up
            dev-container exec
            dev-container exec ls -la
            dev-container down
            dev-container ps
            dev-container status

          Persistent state:
            Shared volume:   $state_volume -> $container_state_dir
            Inside volume:   PostgreSQL data in $container_state_dir/postgresql
          EOF
          }

          cmd_up() {
            local rebuild=false
            local no_cache=false

            while [[ $# -gt 0 ]]; do
              case $1 in
                --rebuild|-r) rebuild=true; shift ;;
                --no-cache) no_cache=true; shift ;;
                -h|--help)
                  echo "Usage: dev-container up [--rebuild|-r] [--no-cache]"
                  echo "  --rebuild, -r   Force rebuild of the image"
                  echo "  --no-cache      Disable Nix cache during build"
                  exit 0
                  ;;
                *) echo "Unknown option: $1"; exit 1 ;;
              esac
            done

            if docker ps --format '{{.Names}}' | grep -q "^$container_name$"; then
              echo ">>> Container '$container_name' is already running"
              echo ">>> Use 'dev-container exec' to enter the container"
              return 0
            fi

            if docker ps -a --format '{{.Names}}' | grep -q "^$container_name$"; then
              echo ">>> Removing stopped container..."
              docker rm -f "$container_name" 2>/dev/null || true
            fi

            local current_version
            current_version=$(git -C "$repo_root" describe --always --dirty 2>/dev/null || date +%s)
            local last_version=""
            [ -f "$version_file" ] && last_version=$(cat "$version_file")

            if ! docker image inspect "$image_name":latest >/dev/null 2>&1 || \
               [ "$rebuild" = true ] || \
               [ "$current_version" != "$last_version" ]; then
              echo ">>> Building container image: $image_name"
              if [ "$no_cache" = true ]; then
                nix build "$flake_ref#packages.$target_system.$package_attr" --option substitute false
              else
                nix build "$flake_ref#packages.$target_system.$package_attr"
              fi
              echo ">>> Loading image into Docker..."
              docker load < result
              echo "$current_version" > "$version_file"
            else
              echo ">>> Using existing image: $image_name:latest"
            fi

            echo ">>> Starting container..."
            echo ">>> Persistent state will be stored in Docker volume: $state_volume"
            local docker_args=(
              -d
              --rm
              --privileged
              --cgroupns=host
              -v /sys/fs/cgroup:/sys/fs/cgroup:rw
              -v "$state_volume:$container_state_dir"
              -p 8080:8080
            )

            if [ -f "$oidc_env_file" ]; then
              echo ">>> Using local OIDC environment file: $oidc_env_file"
              docker_args+=(-v "$oidc_env_file:$container_oidc_env_file:ro")
            else
              echo ">>> No local OIDC environment file found at $oidc_env_file"
              echo ">>> Continuing with default OIDC config inside the dev container"
            fi

            docker run "''${docker_args[@]}" \
              --name "$container_name" \
              "$image_name":latest >/dev/null

            echo ">>> Container '$container_name' started successfully"
            echo ">>> Use 'dev-container exec' to enter the container"
            echo ">>> Use 'dev-container status' to check status"
            echo ">>> Persistent state is stored in Docker volume: $state_volume"
          }

          cmd_exec() {
            local tty_flag=""
            local shell_path="/run/current-system/sw/bin/bash"
            [ -t 0 ] && tty_flag="-t"

            if ! docker ps --format '{{.Names}}' | grep -q "^$container_name$"; then
              echo ">>> Container '$container_name' is not running"
              echo ">>> Run 'dev-container up' first"
              exit 1
            fi

            if [ $# -gt 0 ]; then
              docker exec -i $tty_flag "$container_name" "$shell_path" -lc "$*"
            else
              docker exec -i $tty_flag "$container_name" "$shell_path"
            fi
          }

          cmd_down() {
            local force=false
            local purge_state=false

            while [[ $# -gt 0 ]]; do
              case $1 in
                --force|-f) force=true; shift ;;
                --purge-state) purge_state=true; shift ;;
                -h|--help)
                  echo "Usage: dev-container down [--force|-f] [--purge-state]"
                  echo "  --force, -f  Force remove without confirmation"
                  echo "  --purge-state  Remove persistent Docker volume used by the dev container"
                  exit 0
                  ;;
                *) echo "Unknown option: $1"; exit 1 ;;
              esac
            done

            if ! docker ps -a --format '{{.Names}}' | grep -q "^$container_name$"; then
              echo ">>> Container '$container_name' is not running or does not exist"

              if docker image inspect "$image_name":latest >/dev/null 2>&1; then
                if [ "$force" = true ]; then
                  docker image rm -f "$image_name":latest
                else
                  read -r -p ">>> Remove image '$image_name:latest'? [y/N]: " confirm
                  if [[ "$confirm" == [yY] || "$confirm" == [yY][eE][sS] ]]; then
                    docker image rm -f "$image_name":latest
                  fi
                fi
              fi

              if [ "$purge_state" = true ]; then
                echo ">>> Removing persistent Docker volume..."
                docker volume rm -f "$state_volume" 2>/dev/null || true
              fi
              return 0
            fi

            if [ "$force" = true ]; then
              echo ">>> Stopping container..."
              docker stop "$container_name" 2>/dev/null || docker kill "$container_name" 2>/dev/null || true
            else
              echo ">>> Stopping container..."
              docker stop "$container_name" || true
            fi

            echo ">>> Container stopped"

            if docker image inspect "$image_name":latest >/dev/null 2>&1; then
              if [ "$force" = true ]; then
                docker image rm -f "$image_name":latest
              else
                read -r -p ">>> Remove image '$image_name:latest'? [y/N]: " confirm
                if [[ "$confirm" == [yY] || "$confirm" == [yY][eE][sS] ]]; then
                  docker image rm -f "$image_name":latest
                fi
              fi
            fi

            if [ "$purge_state" = true ]; then
              echo ">>> Removing persistent Docker volume..."
              docker volume rm -f "$state_volume" 2>/dev/null || true
            fi
          }

          cmd_ps() {
            local all=false
            local quiet=false

            while [[ $# -gt 0 ]]; do
              case $1 in
                --all|-a) all=true; shift ;;
                --quiet|-q) quiet=true; shift ;;
                -h|--help)
                  echo "Usage: dev-container ps [--all|-a] [--quiet|-q]"
                  echo "  --all, -a      Show all containers (including stopped)"
                  echo "  --quiet, -q    Only show container names/IDs"
                  exit 0
                  ;;
                *) echo "Unknown option: $1"; exit 1 ;;
              esac
            done

            local ps_format="table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.RunningFor}}"
            local docker_ps_cmd=(docker ps)
            if [ "$all" = false ]; then
              docker_ps_cmd+=(--filter "status=running")
            else
              docker_ps_cmd+=(-a)
            fi
            local containers
            containers=$("''${docker_ps_cmd[@]}" --format "{{.Names}}" | grep "^$container_name" || true)

            if [ -z "$containers" ]; then
              if [ "$quiet" = true ]; then
                :
              else
                echo ">>> No dev containers found"
              fi
              return 0
            fi

            if [ "$quiet" = true ]; then
              echo "$containers"
            else
              echo ">>> Dev Containers"
              echo "=================="
              "''${docker_ps_cmd[@]}" --filter "name=$container_name" --format "$ps_format"
            fi
          }

          cmd_status() {
            echo ">>> Container Status"
            echo "===================="

            if docker ps --format '{{.Names}}' | grep -q "^$container_name$"; then
              echo "Status:    Running"
              docker inspect --format='Started:   {{.State.StartedAt}}' "$container_name"
              docker inspect --format='Health:    {{.State.Health.Status}}' "$container_name" 2>/dev/null || echo "Health:    N/A"
            elif docker ps -a --format '{{.Names}}' | grep -q "^$container_name$"; then
              echo "Status:    Stopped"
            else
              echo "Status:    Not created"
            fi

            if docker image inspect "$image_name":latest >/dev/null 2>&1; then
              echo "Image:     Available"
              echo "Created:   $(docker inspect --format='{{.Created}}' "$image_name":latest | cut -dT -f1)"
            else
              echo "Image:     Not found"
            fi

            echo "State:     docker volume $state_volume -> $container_state_dir"
          }

          if [ $# -eq 0 ]; then
            usage
            exit 1
          fi

          COMMAND="$1"
          shift

          case "$COMMAND" in
            up)     cmd_up "$@" ;;
            exec)   cmd_exec "$@" ;;
            down)   cmd_down "$@" ;;
            ps)     cmd_ps "$@" ;;
            status) cmd_status "$@" ;;
            -h|--help)
              usage
              exit 0
              ;;
            *)
              echo "Unknown command: $COMMAND"
              usage
              exit 1
              ;;
          esac
        '';
      }}/bin/dev-container";
    };
  };
}
