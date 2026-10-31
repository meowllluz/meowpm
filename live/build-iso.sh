#!/bin/sh
# Meow Linux — сборка live ISO (UEFI, systemd-boot).
# Требует root (chroot + mkfs + mount). Запуск: sudo ./live/build-iso.sh
set -eu

MEOW_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LIVE_DIR="$MEOW_DIR/live"
WORK="${WORK:-$MEOW_DIR/.live-rootfs}"
ISO_OUT="${ISO_OUT:-$MEOW_DIR/meow-live.iso}"
ROOTFS_TAR="$MEOW_DIR/rootfs.tar.zst"

[ -f "$ROOTFS_TAR" ] || { echo "нет $ROOTFS_TAR — сначала собери rootfs"; exit 1; }

# --- 1. live rootfs = установочный rootfs + live-пакеты ---
echo "== 1/5 live rootfs =="
rm -rf "$WORK"; mkdir -p "$WORK"
zstd -dc "$ROOTFS_TAR" | tar -x -C "$WORK"

# rootfs.tar.zst запакован БЕЗ dev/proc/sys/run/tmp — создаём точки заново
mkdir -p "$WORK/dev" "$WORK/proc" "$WORK/sys" "$WORK/run" "$WORK/tmp" "$WORK/var/tmp"

mount --bind /dev "$WORK/dev"; mount --bind /proc "$WORK/proc"; mount --bind /sys "$WORK/sys"
cp /etc/resolv.conf "$WORK/etc/resolv.conf"

install -m 0755 "$LIVE_DIR/meow-install" "$WORK/usr/bin/meow-install"
cp "$ROOTFS_TAR" "$WORK/rootfs.tar.zst"

chroot "$WORK" /bin/sh -eu <<'EOF'
meow sync
meow install -y linux iwd dbus seatd wpa_supplicant squashfs-tools cryptsetup busybox-static
EOF
umount "$WORK/dev" "$WORK/proc" "$WORK/sys"

# --- 2. squashfs ---
echo "== 2/5 squashfs =="
mksquashfs "$WORK" "$LIVE_DIR/root.squashfs" -comp zstd -noappend

# --- 3. initramfs (busybox static + init) ---
echo "== 3/5 initramfs =="
BB="$WORK/bin/busybox.static"
[ -e "$BB" ] || BB="$(find "$WORK" -name 'busybox*' -type f 2>/dev/null | head -1)"
[ -n "$BB" ] || { echo "нет busybox static"; exit 1; }
IR="/tmp/meow-initramfs"; rm -rf "$IR"; mkdir -p "$IR/bin" "$IR/sbin" "$IR/proc" "$IR/sys" "$IR/dev" "$IR/run"
cp "$BB" "$IR/bin/busybox"
cp "$LIVE_DIR/init" "$IR/init"; chmod +x "$IR/init"
for a in switch_root mount umount mkdir; do ln -sf /bin/busybox "$IR/bin/$a"; done
(cd "$IR" && find . | cpio -o -H newc 2>/dev/null | zstd -o "$LIVE_DIR/initramfs.img")
rm -rf "$IR"

# --- 4. ядро + systemd-boot ---
echo "== 4/5 ядро + bootloader =="
KERNEL="$(ls "$WORK"/boot/vmlinuz-* 2>/dev/null | head -1)"
[ -n "$KERNEL" ] || { echo "нет ядра (vmlinuz) в live rootfs"; exit 1; }
SDBOOT="$(find "$WORK" -name 'systemd-bootx64.efi' 2>/dev/null | head -1)"
[ -n "$SDBOOT" ] || { echo "нет systemd-bootx64.efi"; exit 1; }

# --- 5. ISO (UEFI) ---
echo "== 5/5 ISO =="
ISO="$LIVE_DIR/.iso"; rm -rf "$ISO"; mkdir -p "$ISO/EFI/BOOT" "$ISO/loader/entries"
cp "$SDBOOT" "$ISO/EFI/BOOT/BOOTX64.EFI"
cp "$KERNEL" "$ISO/vmlinuz"
cp "$LIVE_DIR/initramfs.img" "$ISO/initramfs.img"
cp "$LIVE_DIR/root.squashfs" "$ISO/root.squashfs"
printf 'default meow\ntimeout 3\n' > "$ISO/loader/loader.conf"
printf 'title Meow Linux (live)\nlinux /vmlinuz\ninitrd /initramfs.img\noptions rw\n' > "$ISO/loader/entries/meow.conf"

EFI="$LIVE_DIR/efiboot.img"; rm -f "$EFI"
dd if=/dev/zero of="$EFI" bs=1M count=64 status=none
mkfs.vfat -n MEOW_EFI "$EFI" >/dev/null
mmd -i "$EFI" ::EFI ::EFI/BOOT ::loader ::loader/entries
mcopy -i "$EFI" "$ISO/EFI/BOOT/BOOTX64.EFI" ::EFI/BOOT/BOOTX64.EFI
mcopy -i "$EFI" "$ISO/vmlinuz" ::vmlinuz
mcopy -i "$EFI" "$ISO/initramfs.img" ::initramfs.img
mcopy -i "$EFI" "$ISO/root.squashfs" ::root.squashfs
mcopy -i "$EFI" "$ISO/loader/loader.conf" ::loader/loader.conf
mcopy -i "$EFI" "$ISO/loader/entries/meow.conf" ::loader/entries/meow.conf

xorriso -as mkisofs -iso-level 3 -o "$ISO_OUT" -e efiboot.img -no-emul-boot "$EFI" "$ISO" >/dev/null 2>&1 \
  || xorriso -as mkisofs -iso-level 3 -o "$ISO_OUT" -e efiboot.img -no-emul-boot -append_partition 2 . "$EFI" "$ISO"

echo "ISO готов: $ISO_OUT"
