#!/bin/bash
# Network for the early-boot SSH session in 46sshd: DHCP plus mDNS, so the
# passphrase prompt answers at den.local like the running system does.

# called by dracut
check() {
    return 0
}

# called by dracut
depends() {
    # bash: root's login shell, and Ubuntu's initrd otherwise has only dash.
    echo systemd-networkd systemd-resolved bash
    return 0
}

# called by dracut
install() {
    inst_simple "$moddir/10-den.network" /etc/systemd/network/10-den.network
    inst_simple "$moddir/mdns.conf" /etc/systemd/resolved.conf.d/den-mdns.conf
}
