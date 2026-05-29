# Static map-entity corpus

Generated, committed per-map entity layouts — `<map>.json`
(`mapents.MapEntities`): item spawns, player spawnpoints, teleport
destinations/sources, and buttons, with type + location.

**Do not edit by hand.** Regenerate from BSP entity lumps with:

```
go run ./mvd-analytics/cmd/mapgen -bsp-dir /path/to/maps \
    -out-dir "" -entities-out mvd-analytics/mapents/data
```

Loaded at analyze time by `mapents.LoadForMap(<map>)` (embedded natively;
fetched by the WASM host). This file is embedded too but ignored by map
lookups, which read `<map>.json`.
