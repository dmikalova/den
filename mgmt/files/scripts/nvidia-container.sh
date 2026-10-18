set -euo pipefail

# The container toolkit is not in Ubuntu's archive, so it comes from NVIDIA's
# own repository. The repository is distro agnostic: the list file resolves
# $(ARCH) and carries no release name, which is why it works on a release
# NVIDIA has not packaged for.
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey |
  gpg --yes --dearmor -o /usr/share/keyrings/nvidia-container-toolkit.gpg

curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list |
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit.gpg] https://#g' \
    >/etc/apt/sources.list.d/nvidia-container-toolkit.list

apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y nvidia-container-toolkit

# The quadlets run rootless, and a rootless container cannot write the device
# cgroup. Without this the GPU is only reachable by root, which would mean
# running the model as root.
nvidia-ctk config --set nvidia-container-cli.no-cgroups --in-place

systemctl daemon-reload
systemctl enable --now nvidia-cdi-refresh.service

touch /var/lib/den/nvidia-container-v1
