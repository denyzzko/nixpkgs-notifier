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

        vendorHash = "sha256-5FEeJRmoEp5yVPwRrLjf7XuBy1NHKwdH/Um+GTAkPqI=";

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
