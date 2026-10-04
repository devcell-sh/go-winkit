{
  description = "winkit home environment built from devcell-sh/home modules";

  inputs = {
    # Reuse home's pins so the modules see the nixpkgs and home-manager
    # they were written against.
    home.url = "github:devcell-sh/home";
    nixpkgs.follows = "home/nixpkgs";
    home-manager.follows = "home/home-manager";
  };

  outputs = { nixpkgs, home-manager, home, ... }: {
    # winkit activates homeConfigurations.<user>; the PE distro user is
    # "winkit" with home /home/winkit, and the guest is Windows on ARM.
    homeConfigurations.winkit = home-manager.lib.homeManagerConfiguration {
      pkgs = import nixpkgs {
        system = "aarch64-linux";
        config.allowUnfree = true; # corefonts in the desktop module
      };
      modules = [
        "${home}/modules/core.nix"
        "${home}/modules/shell.nix"
        "${home}/modules/desktop"
        ({ pkgs, ... }: {
          home.stateVersion = "25.11";
          home.username = "winkit";
          home.homeDirectory = "/home/winkit";

          # winkit's rootfs build copies sudo out of this profile with the
          # setuid bit (nix cannot install setuid binaries itself), and
          # the in-distro sshd is OpenSSH. The embedded default home.nix
          # carries both; a composed flake has to say so.
          # gnused and gawk: ~/.icewm/startup (from the home desktop module)
          # shells out to sed, and nothing else in the profile provides it.
          home.packages = [
            pkgs.sudo pkgs.openssh pkgs.gnused pkgs.gawk
            pkgs.chromium
          ];

          # The desktop module as devcell's ultimate stack configures it:
          # IceWM + Xvfb + x11vnc + fonts. The heavy optional parts stay
          # off so the rootfs tarball and data disk stay small.
          devcell.modules.desktop = {
            enable = true;
            windowManager = "icewm";
            nativeUi.enable = false;
            rdpClient.enable = false;
            kitty.enable = false;
            wxWidgets.enable = false;
          };
        })
      ];
    };
  };
}
