{
  inputs = {
    nixpkgs.url = "nixpkgs";
    utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      utils,
    }:
    utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "gitlab-reviewer";
          version = "0.1.0";
          src = ./.;
          vendorHash = null;
        };

        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go ];
        };
      }
    );
}
