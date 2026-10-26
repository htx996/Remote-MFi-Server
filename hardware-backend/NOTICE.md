# CH341/MFi hardware helper notice

The source code under `hardware-backend/` is a modified independent program
distributed under the GNU General Public License version 3 only. See `LICENSE`.

Protocol and transport behavior was studied from these upstream projects:

- `shilapi/xcertplay` at commit
  `1298fb35e29ee30e7bd849cb0e751b52aa22a13f`
  (<https://github.com/shilapi/xcertplay>).
- `cuckoohello/remote-mfi-for-xcertplay` at commit
  `b7bd0852539c00b36c7cd850990abb64cf300ead`
  (<https://github.com/cuckoohello/remote-mfi-for-xcertplay>), licensed
  GPL-3.0-only.

This implementation was modified for Remote-MFi-Server on 2026-10-26. It adds
validated environment configuration, a constrained subprocess JSON contract,
configured-address-only business operations, bounded input/output, cancellation,
and container-oriented libusb lifecycle handling.

The Python FastAPI service is a separate program. It communicates with this
helper only through command-line arguments, JSON stdin/stdout, process exit
status, and a subprocess boundary. The distributed Docker image is an aggregate.
It contains the helper source, vendored Go dependencies and licenses, and the
Docker build recipe under /usr/share/doc/remote-mfi. The exact source revision is
shown by `mfi-backend --version` and OCI metadata. Source repository:
https://github.com/htx996/Remote-MFi-Server.
