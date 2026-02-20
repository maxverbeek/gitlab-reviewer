{
  inputs = {
    nixpkgs.url = "nixpkgs";
  };

  outputs =
    { self, nixpkgs, ... }:
    let
      systems = [ "x86_64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      nixpkgsFor = forAllSystems (
        system:
        import nixpkgs {
          inherit system;
          overlays = [ self.overlays.default ];
        }
      );

      gitlab-reviewer =
        { buildGoModule }:
        buildGoModule {
          pname = "gitlab-reviewer";
          version = "0.1.0";
          src = ./.;
          vendorHash = null;
        };
    in
    {
      overlays.default = final: prev: {
        gitlab-reviewer = final.callPackage gitlab-reviewer { };
      };

      packages = forAllSystems (system: {
        default = nixpkgsFor.${system}.gitlab-reviewer;
      });

      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgsFor.${system};
        in
        {
          default = pkgs.mkShell {
            packages = [ pkgs.go ];
          };
        }
      );
    };
}
