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

        vendorHash = "sha256-3QvQ2v6LybqJ3/Kaa1bCY7aAoR/aU4kWeyPM9SfZKME=";

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
