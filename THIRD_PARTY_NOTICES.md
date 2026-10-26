# Third-party notices

## CH341F/MFi337 hardware helper

`hardware-backend/` is a modified independent Go program distributed under
GPL-3.0-only. Its complete source and license are included in this repository at
`hardware-backend/` and `hardware-backend/LICENSE`.

The helper's CH341 USB/I2C and MFi authentication-coprocessor behavior is based
on study and modification of:

- [shilapi/xcertplay](https://github.com/shilapi/xcertplay), commit
  `1298fb35e29ee30e7bd849cb0e751b52aa22a13f`.
- [cuckoohello/remote-mfi-for-xcertplay](https://github.com/cuckoohello/remote-mfi-for-xcertplay),
  commit `b7bd0852539c00b36c7cd850990abb64cf300ead`, GPL-3.0-only.

The Python FastAPI service and the GPL helper do not link at the language or
library level. They communicate only through a subprocess interface consisting
of argv, JSON stdin/stdout, and an exit status. The Docker image aggregates both
programs and includes the helper's corresponding source and notices.
