#!/bin/sh
# Meow Linux — сборка live ISO (UEFI, systemd-boot, dracut initramfs + dmsquash-live).
# dracut сам кладёт модули (squashfs/overlay/vfat/iso9660/device-mapper) в initramfs.
# Требует root (chroot + mkfs + mount). Запуск: sudo ./live/build-iso.sh
set -eu

MEOW_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LIVE_DIR="$MEOW_DIR/live"
WORK="${WORK:-$MEOW_DIR/.live-rootfs}"
ISO_OUT="${ISO_OUT:-$MEOW_DIR/meow-live.iso}"
ROOTFS_TAR="$MEOW_DIR/rootfs.tar.zst"

[ "$(id -u)" = 0 ] || { echo "build-iso: запускай через sudo" >&2; exit 1; }
[ -f "$ROOTFS_TAR" ] || { echo "нет $ROOTFS_TAR — сначала собери rootfs"; exit 1; }

# архив установщика должен быть root-овым и с setuid, иначе поставится битая система
ARCHIVE_LIST="$(zstd -dc "$ROOTFS_TAR" | tar -tvf -)"
BAD_OWNER="$(printf '%s\n' "$ARCHIVE_LIST" | awk '$2 != "0/0" {c++} END {print c+0}')"
SUID_COUNT="$(printf '%s\n' "$ARCHIVE_LIST" | awk '$1 ~ /^-/ && $1 ~ /s/ {c++} END {print c+0}')"
[ "$BAD_OWNER" = 0 ] || { echo "build-iso: rootfs.tar.zst собран не от root ($BAD_OWNER записей)" >&2; exit 1; }
[ "$SUID_COUNT" -ge 3 ] || { echo "build-iso: в rootfs.tar.zst нет setuid-битов ($SUID_COUNT)" >&2; exit 1; }
echo "   архив: владельцы 0/0, setuid-файлов $SUID_COUNT"

# --- 1. live rootfs = установочный rootfs + live-пакеты (включая dracut) ---
echo "== 1/5 live rootfs =="
rm -rf "$WORK"; mkdir -p "$WORK"
zstd -dc "$ROOTFS_TAR" | tar -x --numeric-owner -C "$WORK"
mkdir -p "$WORK/dev" "$WORK/proc" "$WORK/sys" "$WORK/run" "$WORK/tmp" "$WORK/var/tmp"

mount --bind /dev "$WORK/dev"; mount --bind /proc "$WORK/proc"; mount --bind /sys "$WORK/sys"
trap 'umount "$WORK/dev" 2>/dev/null; umount "$WORK/proc" 2>/dev/null; umount "$WORK/sys" 2>/dev/null' EXIT
cp /etc/resolv.conf "$WORK/etc/resolv.conf"

install -m 0755 "$LIVE_DIR/meow-install" "$WORK/usr/bin/meow-install"
cp "$ROOTFS_TAR" "$WORK/rootfs.tar.zst"

chroot "$WORK" /bin/sh -eu <<'EOF'
meow sync
meow install -y linux iwd dbus seatd wpa_supplicant squashfs-tools cryptsetup systemd-boot dracut device-mapper
echo 'LANG=C' > /etc/locale.conf
echo 'root:meow' | chpasswd -c SHA512
EOF

# включить сервисы по умолчанию: dbus (нужен iwd), iwd (wifi), dhcpcd (сеть/DNS), seatd
for s in dbus iwd dhcpcd seatd; do
    ln -sf "/etc/sv/$s" "$WORK/etc/runit/runsvdir/default/$s"
done

# --- 2. initramfs через dracut (dmsquash-live монтирует squashfs по label) ---
echo "== 2/5 initramfs (dracut dmsquash-live) =="
KVER="$(ls "$WORK/usr/lib/modules" 2>/dev/null | head -1)"
[ -n "$KVER" ] || { echo "build-iso: в live rootfs нет /usr/lib/modules — ядро не установилось" >&2; exit 1; }
echo "   ядро: $KVER"
chroot "$WORK" /usr/bin/dracut -N --force --add "dmsquash-live" --omit systemd "/boot/initrd" "$KVER"

umount "$WORK/dev" "$WORK/proc" "$WORK/sys"

# --- 3. squashfs → LiveOS/squashfs.img (dmsquash-live ожидает его там) ---
echo "== 3/5 squashfs =="
rm -rf "$LIVE_DIR/.iso"
mkdir -p "$LIVE_DIR/.iso/LiveOS"
rm -f "$LIVE_DIR/.iso/LiveOS/squashfs.img"
mksquashfs "$WORK" "$LIVE_DIR/.iso/LiveOS/squashfs.img" -comp zstd -noappend

# --- 4. ядро + initrd + systemd-boot ---
echo "== 4/5 ядро + bootloader =="
KERNEL="$(ls "$WORK"/boot/vmlinuz-* 2>/dev/null | head -1)"
[ -n "$KERNEL" ] || { echo "нет ядра (vmlinuz)"; exit 1; }
INITRD="$WORK/boot/initrd"
[ -f "$INITRD" ] || { echo "нет initrd"; exit 1; }
SDBOOT="$(find "$WORK" -name 'systemd-bootx64.efi' | head -1)"
[ -n "$SDBOOT" ] || { echo "нет systemd-bootx64.efi"; exit 1; }

# --- 5. ISO (UEFI) ---
echo "== 5/5 ISO =="
ISO="$LIVE_DIR/.iso"
cp "$KERNEL" "$ISO/vmlinuz"
cp "$INITRD" "$ISO/initrd"

LCONF="$LIVE_DIR/.loader.conf"; ECONF="$LIVE_DIR/.meow.conf"
printf 'default meow\ntimeout 3\n' > "$LCONF"
printf 'title Meow Linux (live)\nlinux /vmlinuz\ninitrd /initrd\noptions root=live:LABEL=MEOW_LIVE rw LANG=C\n' > "$ECONF"

EFI="$LIVE_DIR/efiboot.img"; rm -f "$EFI"
EFI_MB=$(( ( $(stat -c %s "$INITRD") + $(stat -c %s "$KERNEL") ) / 1048576 + 32 ))
dd if=/dev/zero of="$EFI" bs=1M count="$EFI_MB" status=none
mkfs.vfat -n MEOW_EFI "$EFI" >/dev/null
mmd -i "$EFI" ::EFI ::EFI/BOOT ::loader ::loader/entries
mcopy -i "$EFI" "$SDBOOT" ::EFI/BOOT/BOOTX64.EFI
mcopy -i "$EFI" "$KERNEL" ::vmlinuz
mcopy -i "$EFI" "$INITRD" ::initrd
mcopy -i "$EFI" "$LCONF" ::loader/loader.conf
mcopy -i "$EFI" "$ECONF" ::loader/entries/meow.conf

xorriso -as mkisofs -iso-level 3 -V MEOW_LIVE -e efiboot.img -no-emul-boot -isohybrid-gpt-basdat -o "$ISO_OUT" "$EFI" "$ISO"
rm -f "$LCONF" "$ECONF"

echo "ISO готов: $ISO_OUT"
