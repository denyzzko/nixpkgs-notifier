{ ... }:
{
  perSystem =
    { pkgs, ... }:
    let
      nixpkgs-notifier = pkgs.buildGoModule {
        pname = "nixpkgs-notifier";
        version = "0.1.0";

        src = ../..;
        subPackages = [ "cmd/server" ];

        vendorHash = "sha256-NTeYEreteIWUymGOj/5VkK8XW78b4u5NGhawRTUZ2pU=";

        nativeBuildInputs = [ pkgs.templ ];

        preBuild = ''
          templ generate
        '';

        ldflags = [ "-s" "-w" ];

        postInstall = ''
          if [ -f "$out/bin/server" ]; then
            mv "$out/bin/server" "$out/bin/nixpkgs-notifier"
          fi
        '';

        meta.mainProgram = "nixpkgs-notifier";
      };
    in
    {
      packages.nixpkgs-notifier = nixpkgs-notifier;
      packages.default = nixpkgs-notifier;
    };
}
