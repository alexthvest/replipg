{ pkgs, ... }:

let
  go_1_26_10 = pkgs.go_1_26.overrideAttrs (_: {
    version = "1.26.10";
    src = pkgs.fetchurl {
      url = "https://go.dev/dl/go1.26.10.src.tar.gz";
      hash = "sha256-/WPog7ovK/ypPBZGsx6+9j4rHk0rMXyXidl2po2B828=";
    };
  });
in
{
  dotenv.enable = true;

  languages.go = {
    enable = true;
    package = go_1_26_10;
  };

  packages = with pkgs; [
    gopls
    golangci-lint
  ];
}
