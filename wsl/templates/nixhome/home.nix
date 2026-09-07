{ pkgs, ... }:
{
  home.username = "@USER@";
  home.homeDirectory = "/home/@USER@";
  home.stateVersion = "25.05";

  programs.home-manager.enable = true;

  programs.bash = {
    enable = true;
    package = pkgs.bash;
    enableCompletion = true;
    initExtra = ''
      export PS1='\u@@DISTRO@:\w\$ '
      export LOCALE_ARCHIVE=/nix/var/nix/profiles/default/lib/locale/locale-archive
      [ -r /etc/profile.d/nix.sh ] && . /etc/profile.d/nix.sh
    '';
  };

  programs.git.enable = true;

  home.packages = with pkgs; [
    coreutils findutils gnugrep
    sudo openssh
    less ripgrep jq
  ];
}
