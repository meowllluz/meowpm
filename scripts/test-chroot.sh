#!/bin/sh
# meowpm — подготовка Void rootfs к ручному тесту переноса xbps -> meow.
#
#   1. Удаляет старую rootfs (без переиспользования).
#   2. Скачивает свежий Void rootfs (glibc, x86_64) и распаковывает.
#   3. Через нативный xbps ставит зависимости meow (curl tar zstd shadow runit)
#      плюс тестовые пакеты tree и htop (их потом и переносим в meow).
#   4. Собирает meow и xbps2meow, копирует в /usr/bin, пишет /etc/meow/pm.conf.
#   5. Монтирует /dev /proc /sys (остаются смонтированными для ручного chroot).
#   6. Печатает команды для ручного теста.
#
# Требует root. Запуск:  sudo ./scripts/test-chroot.sh
# Alt-root:  ROOT=/tmp/void sudo ./scripts/test-chroot.sh
# Путь к xbps2meow:  X2M_DIR=/path sudo ./scripts/test-chroot.sh
set -eu

MEOW_DIR="$(cd "$(dirname "$0")/.." && pwd)"
X2M_DIR="${X2M_DIR:-/home/llluz/xbps2meow}"
ROOT="${ROOT:-$MEOW_DIR/.test-rootfs}"
REPO="https://repo-default.voidlinux.org/current"
LIVE="https://repo-default.voidlinux.org/live/current"

echo "== 1/6 удаление старой rootfs ($ROOT) =="
rm -rf "$ROOT"
mkdir -p "$ROOT"

echo "== 2/6 скачивание и распаковка =="
ROOTFS_URL="$(curl -fsSL "$LIVE/" | grep -oE 'void-x86_64-ROOTFS-[0-9]+\.tar\.xz' | sort -V | tail -1)"
ROOTFS_URL="$LIVE/$ROOTFS_URL"
echo "    $ROOTFS_URL"
curl -fsSL "$ROOTFS_URL" | tar -xJ -C "$ROOT"

cp /etc/resolv.conf "$ROOT/etc/resolv.conf"

echo "== 3/6 bootstrap зависимостей + тестовые пакеты через xbps =="
chroot "$ROOT" xbps-install -Syu xbps || true
chroot "$ROOT" xbps-install -Sy curl tar zstd shadow runit
chroot "$ROOT" xbps-install -Sy tree htop

echo "== 4/6 сборка meow и xbps2meow =="
(cd "$MEOW_DIR" && go build -o "$ROOT/usr/bin/meow" .)
(cd "$X2M_DIR" && go build -o "$ROOT/usr/bin/xbps2meow" .)

mkdir -p "$ROOT/etc/meow"
cat > "$ROOT/etc/meow/pm.conf" <<EOF
REPO_MAIN=$REPO
ARCH=x86_64
ESCALATOR=doas
EOF

echo "== 5/6 mount /dev /proc /sys =="
mount --bind /dev "$ROOT/dev" || true
mount --bind /proc "$ROOT/proc" || true
mount --bind /sys "$ROOT/sys" || true

cat <<EOF

== 6/6 готово ==
Среда подготовлена. Войди в chroot:

    sudo chroot $ROOT /bin/sh

Внутри — команды теста переноса (по порядку):

    # 1. перенос xbps-БД в meow
    xbps2meow

    # 2. meow должен видеть ВСЕ пакеты (не только tree/htop)
    meow list

    # 3. dry-run удаления: НЕ должен тянуть glibc за собой
    meow remove -n tree

    # 4. реальное удаление tree (без -R, чтобы не каскадить)
    meow remove -y tree

    # 5. установить что-то через meow поверх перенесённой БД
    meow install -y tree

    # 6. снова список
    meow list

Когда закончишь — exit из chroot и отмонтируй:

    sudo umount $ROOT/dev $ROOT/proc $ROOT/sys
EOF
