{ pkgs, ... }:

{
  dotenv.enable = true;

  languages.go = {
    enable = true;
  };

  packages = with pkgs; [
    gopls
    golangci-lint
  ];
}
