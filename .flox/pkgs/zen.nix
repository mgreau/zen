{
  buildGoModule,
  go_1_25,
  lib,
  makeWrapper,
  git,
  gh,
}:

# Pin Go 1.25 here so both `flox build` (catalog pkgs) and the flake
# (callPackage) get it. go.mod requires go 1.25.7; an unpinned
# buildGoModule can be older and fail in the sandbox (toolchain fetch).
(buildGoModule.override { go = go_1_25; }) {
  pname = "zen";
  version = "dev";

  src = ../../.;

  # Keep in sync with go.sum. `flox build` / `nix build` prints the correct
  # hash when this is empty or wrong.
  vendorHash = "sha256-d0muw8O2bIftdNNXZBxrpgjsZM/NjBL09icanyVTA6Q=";

  env.CGO_ENABLED = "0";

  ldflags = [
    "-s"
    "-w"
    "-X github.com/mgreau/zen/cmd.Version=dev"
    "-X github.com/mgreau/zen/cmd.Commit=unknown"
  ];

  nativeBuildInputs = [ makeWrapper ];

  # TestCreateFromMain shells out to `git`; the sandboxed checkPhase has no
  # PATH beyond nativeCheckInputs, so `git` must be listed here too (it's
  # otherwise only reachable at runtime via postInstall's wrapProgram).
  nativeCheckInputs = [ git ];

  # zen execs git and gh; wrap them so the binary works without a project env.
  postInstall = ''
    wrapProgram $out/bin/zen --prefix PATH : ${lib.makeBinPath [ git gh ]}
  '';

  meta = {
    description = "Worktree orchestrator for AI-assisted PR reviews and feature work";
    homepage = "https://github.com/mgreau/zen";
    license = lib.licenses.mit;
    mainProgram = "zen";
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
}
