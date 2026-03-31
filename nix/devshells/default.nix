{ ... }:
{
  perSystem = { pkgs, ... }: {
    devShells.default = pkgs.mkShell {
      packages = [
        pkgs.git
        pkgs.go
        pkgs.gopls
        pkgs.gotools
        pkgs.templ
      ];

      shellHook = ''
        echo "nixpkgs-notifier dev shell - Go $(go version | awk '{print $3}')"
      '';
    };
  };
}
