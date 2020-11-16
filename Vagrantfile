# path to wg0.conf, mount it to /srv/wg0.conf
HOST_PATH = '/Users/xatier/wg/'
GUEST_PATH = '/srv/'
PORT = 1081

Vagrant.configure("2") do |config|
  config.vm.box = "archlinux/archlinux"
  config.vm.hostname = 'wg'

  config.vm.synced_folder HOST_PATH, GUEST_PATH

  config.vm.network :forwarded_port, guest: PORT, host: PORT

  config.vm.provision "shell", privileged: false, inline: <<-SHELL
    set -x -euo pipefail

    sudo pacman -Syuu --noconfirm --needed \
        git \
        go \
        mtr \
        pacman-contrib \
        python \
        sudo \
        vim \
        wget \
        wireguard-tools

    sudo wg-quick up /srv/wg0.conf
    curl -Ss ipinfo.io

    rm -rf toy-socks5
    git clone https://github.com/xatier/toy-socks5.git
    cd toy-socks5

    go build

  SHELL

  config.vm.provider :virtualbox do |v|
      v.customize ["modifyvm", :id, "--cpus", 1]
      v.customize ["modifyvm", :id, "--memory", 256]
  end

end
