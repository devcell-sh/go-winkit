package wsl

// BaseRecipe builds a WSL rootfs from an arbitrary docker image ref
// (ubuntu:24.04, ghcr.io/org/img:tag, …) using the universal plumbing
// template (templates/Dockerfile.base.tmpl): wsl.conf, fstab, login-shell
// wrapper always; packages, user, sudo, and in-distro sshd best-effort per
// the base's package manager.
func BaseRecipe(baseImage, user, distroName string) (Recipe, error) {
	if distroName == "" {
		distroName = "winkit"
	}
	df, err := renderDockerfile("Dockerfile.base.tmpl", baseImage)
	if err != nil {
		return Recipe{}, err
	}
	r := Recipe{
		Image:          imageSlug(baseImage),
		User:           user,
		DistroName:     distroName,
		Dockerfile:     df,
		VerifyCommand:  "uname -a",
		VerifyContains: "Linux",
	}
	// The template COPYs s6/ into /etc/s6/services unconditionally, so the
	// context must always carry at least the sshd built-in.
	if err := r.AddService(SSHDBaseService()); err != nil {
		return Recipe{}, err
	}
	return r, nil
}
