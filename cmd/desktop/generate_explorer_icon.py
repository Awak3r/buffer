"""Regenerate the Explorer icon after updating the tray PNGs.

Run `python3 cmd/desktop/generate_explorer_icon.py` with MinGW-w64 windres
on PATH (or point WINDRES at it). Tested with GNU Binutils 2.41.90.20240122.
The checked-in .syso is linked by Go on Windows amd64; normal builds do not
need Python or windres. Only RT_ICON and RT_GROUP_ICON are emitted.
"""

import os
from pathlib import Path
import struct
import subprocess
import tempfile


RT_ICON = 3
RT_GROUP_ICON = 14


def resource(kind, ident, data):
    # Win32 .res: ordinal type/name, then zeroed metadata (neutral language).
    header = struct.pack("<IIHHHHIHHII", len(data), 32, 0xFFFF, kind, 0xFFFF, ident, 0, 0, 0, 0, 0)
    return header + data + bytes(-len(data) % 4)


def main():
    root = Path(__file__).resolve().parent
    entries = [resource(0, 0, b"")]  # Required empty .res header.
    sizes = (16, 32)
    group = [struct.pack("<HHH", 0, 1, len(sizes))]  # Icon group header.
    for ident, size in enumerate(sizes, start=1):
        png = (root / f"tray_icon_{size}.png").read_bytes()
        ihdr = b"\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR" + struct.pack(">II", size, size)
        if not png.startswith(ihdr):
            raise ValueError(f"expected a {size}x{size} tray PNG")
        entries.append(resource(RT_ICON, ident, png))
        # GRPICONDIRENTRY references the RT_ICON ID instead of an ICO file offset.
        group.append(struct.pack("<BBBBHHIH", size, size, 0, 0, 1, 32, len(png), ident))
    entries.append(resource(RT_GROUP_ICON, 1, b"".join(group)))

    with tempfile.TemporaryDirectory() as tmp:
        src = Path(tmp) / "icons.res"
        dst = Path(tmp) / "icons.syso"
        src.write_bytes(b"".join(entries))
        subprocess.run([
            os.environ.get("WINDRES", "x86_64-w64-mingw32-windres"),
            "-F", "pe-x86-64", "-J", "res", "-O", "coff", "-i", str(src), "-o", str(dst),
        ], check=True)
        (root / "explorer_icon_windows_amd64.syso").write_bytes(dst.read_bytes())


if __name__ == "__main__":
    main()
