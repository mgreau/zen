{
  description = "zen — worktree orchestrator for AI-assisted PR reviews";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      gitCommit = self.shortRev or self.dirtyShortRev or "dirty";
      version = self.shortRev or self.dirtyShortRev or "dev";

      mkZen =
        pkgs:
        (pkgs.callPackage ./.flox/pkgs/zen.nix { }).overrideAttrs
          {
            inherit version;
            src = self;
            ldflags = [
              "-s"
              "-w"
              "-X github.com/mgreau/zen/cmd.Version=${version}"
              "-X github.com/mgreau/zen/cmd.Commit=${gitCommit}"
            ];
          };
    in
    {
      overlays.default = final: _prev: {
        zen = mkZen final;
      };

      packages = forAllSystems (
        system:
        let
          zen = mkZen nixpkgs.legacyPackages.${system};
        in
        {
          inherit zen;
          default = zen;
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/zen";
        };
      });
    };
}
