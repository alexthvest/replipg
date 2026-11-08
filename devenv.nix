{ pkgs, ... }:

{
  dotenv.enable = true;

  languages.go = {
    enable = true;
  };

  packages = with pkgs; [
    golangci-lint
    cue
    openspec
  ];
}
