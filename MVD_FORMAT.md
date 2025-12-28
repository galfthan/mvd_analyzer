# MVD Demo Format Specification

## Overview

MVD (Multi-View Demo) is a demo recording format for QuakeWorld that captures the complete game state from the server's perspective. Unlike QWD (QuakeWorld Demo) which records a single player's view, MVD records all players simultaneously, allowing spectators to switch between any player's point of view during playback.

### Key Characteristics

- **Server-side recording**: Captures all player states and game events
- **Multi-view support**: Viewer can switch between any player
- **Delta compression**: Only changed values are transmitted
- **Streaming support**: Can be streamed via QTV (QuakeTV) protocol
- **Time representation**: Millisecond deltas (not absolute time like QWD)

### File Extensions

| Extension | Description |
|-----------|-------------|
| `.mvd` | Standard MVD demo file |
| `.mvd.gz` | Gzip-compressed MVD |
| `.mvd.bz2` | Bzip2-compressed MVD |

---

## Binary Format Conventions

All multi-byte values are stored in **little-endian** byte order.

### Data Types

| Type | Size | Description |
|------|------|-------------|
| `byte` | 1 | Unsigned 8-bit integer (0-255) |
| `char` | 1 | Signed 8-bit integer (-128 to 127) |
| `short` | 2 | Signed 16-bit integer |
| `ushort` | 2 | Unsigned 16-bit integer |
| `long` / `int` | 4 | Signed 32-bit integer |
| `float` | 4 | IEEE 754 single-precision float |
| `coord` | 2 | Position coordinate: `value * 8` (gives 1/8 unit precision) |
| `angle` | 1 | Angle: `value * (360.0 / 256.0)` degrees |
| `angle16` | 2 | Angle: `value * (360.0 / 65536.0)` degrees |
| `string` | variable | Null-terminated ASCII string |

### Coordinate Encoding

Standard coordinates are encoded as 16-bit signed integers:
```
encoded_value = (int)(real_value * 8)
real_value = encoded_value / 8.0
```

With `FTE_PEXT_FLOATCOORDS` or `MVD_PEXT1_FLOATCOORDS`, coordinates are 32-bit floats.

---

## File Structure

An MVD file is a sequential stream of **demo messages**. There is no file header - parsing begins immediately with the first message.

```
[Message 1]
[Message 2]
[Message 3]
...
[End Message]
```

---

## Message Structure

Each message consists of a header followed by type-specific payload.

### Message Header

```
Offset  Size  Field
------  ----  -----
0       1     time_delta    - Milliseconds since previous message (0-255)
1       1     message_type  - Message type (see below)
```

The `message_type` byte encodes both the type and sometimes additional data:

```
Bits 0-2: Message type (0-7)
Bits 3-7: Additional data (for dem_single and dem_stats: player number)
```

### Message Types

| Value | Name | Description |
|-------|------|-------------|
| 0 | `dem_cmd` | User command (QWD only, not used in MVD) |
| 1 | `dem_read` | Network message packet |
| 2 | `dem_set` | Sequence numbers (once at demo start) |
| 3 | `dem_multiple` | Message for multiple players (MVD only) |
| 4 | `dem_single` | Message for single player (MVD only) |
| 5 | `dem_stats` | Stats update for player (MVD only) |
| 6 | `dem_all` | Message for all players (MVD only) |

---

## Message Type Details

### dem_set (type 2)

Appears **once** at the beginning of the demo. Sets initial sequence numbers.

```
Offset  Size  Field
------  ----  -----
0       1     time_delta (usually 0)
1       1     message_type (2)
2       4     incoming_sequence (little-endian long)
6       4     outgoing_sequence (little-endian long)
```

**Total size**: 10 bytes

### dem_all (type 6)

Message broadcast to all players. Most common message type.

```
Offset  Size  Field
------  ----  -----
0       1     time_delta
1       1     message_type (6)
2       4     payload_size (little-endian long)
6       N     payload (network message data)
```

**Total size**: 6 + payload_size bytes

### dem_multiple (type 3)

Message directed to specific players, identified by a bitmask.

```
Offset  Size  Field
------  ----  -----
0       1     time_delta
1       1     message_type (3)
2       4     player_mask (little-endian long, 32 bits = 32 players)
6       4     payload_size (little-endian long)
10      N     payload (network message data)
```

**Total size**: 10 + payload_size bytes

**Player mask interpretation**: Each bit represents a player slot (0-31). If bit N is set, player N should receive this message.

**Special case - Hidden messages**: When `player_mask == 0`, the message is a "hidden" message containing metadata not displayed to any player. See [Hidden Messages](#hidden-messages) section.

### dem_single (type 4)

Message for a single player. Player number encoded in header byte.

```
Header byte structure:
  Bits 0-2: message_type (4)
  Bits 3-7: player_number (0-31)

Offset  Size  Field
------  ----  -----
0       1     time_delta
1       1     message_type_and_player (type in bits 0-2, player in bits 3-7)
2       4     payload_size (little-endian long)
6       N     payload (network message data)
```

**Extracting player number**:
```c
int message_type = header_byte & 0x07;      // bits 0-2
int player_number = header_byte >> 3;        // bits 3-7
```

**Total size**: 6 + payload_size bytes

### dem_stats (type 5)

Stats update for a specific player. Same encoding as dem_single.

```
Header byte structure:
  Bits 0-2: message_type (5)
  Bits 3-7: player_number (0-31)

Offset  Size  Field
------  ----  -----
0       1     time_delta
1       1     message_type_and_player
2       4     payload_size (little-endian long)
6       N     payload (svc_updatestat messages)
```

**Total size**: 6 + payload_size bytes

---

## Network Message Payload

The payload in dem_read, dem_all, dem_multiple, dem_single, and dem_stats messages contains standard QuakeWorld network messages (svc_* commands).

### Server Command Types (svc_*)

| Value | Name | Description |
|-------|------|-------------|
| 0 | `svc_bad` | Invalid |
| 1 | `svc_nop` | No operation |
| 2 | `svc_disconnect` | Disconnect message |
| 3 | `svc_updatestat` | Update player stat (byte value) |
| 6 | `svc_sound` | Play sound |
| 8 | `svc_print` | Print message |
| 9 | `svc_stufftext` | Stuff command into console |
| 10 | `svc_setangle` | Set view angle |
| 11 | `svc_serverdata` | Server initialization data |
| 12 | `svc_lightstyle` | Set light style |
| 14 | `svc_updatefrags` | Update player frags |
| 20 | `svc_spawnstatic` | Spawn static entity |
| 22 | `svc_spawnbaseline` | Entity baseline |
| 23 | `svc_temp_entity` | Temporary entity (explosion, etc.) |
| 24 | `svc_setpause` | Pause game |
| 26 | `svc_centerprint` | Center screen message |
| 30 | `svc_intermission` | Intermission screen |
| 32 | `svc_cdtrack` | CD track number |
| 36 | `svc_updateping` | Update player ping |
| 37 | `svc_updateentertime` | Update player enter time |
| 38 | `svc_updatestatlong` | Update player stat (long value) |
| 40 | `svc_updateuserinfo` | Update player userinfo |
| 42 | `svc_playerinfo` | Player state update |
| 43 | `svc_nails` | Nail projectiles |
| 44 | `svc_chokecount` | Choked packet count |
| 45 | `svc_modellist` | Model precache list |
| 46 | `svc_soundlist` | Sound precache list |
| 47 | `svc_packetentities` | Entity updates |
| 48 | `svc_deltapacketentities` | Delta entity updates |
| 51 | `svc_setinfo` | Set player info key |
| 52 | `svc_serverinfo` | Set server info key |
| 53 | `svc_updatepl` | Update packet loss |
| 54 | `svc_nails2` | Nail projectiles (MVD extended) |

---

## svc_serverdata (11) - Server Initialization

First message in the demo, contains complete server setup.

```
Offset  Size  Field
------  ----  -----
0       1     svc_serverdata (11)

// Optional protocol extensions (repeat until PROTOCOL_VERSION seen)
1       4     extension_id (e.g., PROTOCOL_VERSION_FTE, PROTOCOL_VERSION_MVD1)
5       4     extension_flags

// Standard header
N       4     PROTOCOL_VERSION (28)
N+4     4     server_count (spawn count)
N+8     var   game_directory (string)
N+?     4     server_time (float)
N+?     var   level_name (string)

// Movement variables
        4     gravity (float)
        4     stopspeed (float)
        4     maxspeed (float)
        4     spectator_maxspeed (float)
        4     accelerate (float)
        4     airaccelerate (float)
        4     wateraccelerate (float)
        4     friction (float)
        4     waterfriction (float)
        4     entgravity (float)
```

### Protocol Extension IDs

| ID | Name | Value |
|----|------|-------|
| `PROTOCOL_VERSION_FTE` | FTE extensions | `'F' + ('T'<<8) + ('E'<<16) + ('X'<<24)` = 0x58455446 |
| `PROTOCOL_VERSION_FTE2` | FTE extensions 2 | `'F' + ('T'<<8) + ('E'<<16) + ('2'<<24)` = 0x32455446 |
| `PROTOCOL_VERSION_MVD1` | MVD extensions | `'M' + ('V'<<8) + ('D'<<16) + ('1'<<24)` = 0x3144564D |
| `PROTOCOL_VERSION` | Standard QW | 28 |

### FTE Protocol Extension Flags

```c
#define FTE_PEXT_TRANS              0x00000008  // Alpha transparency
#define FTE_PEXT_HLBSP              0x00000200  // Half-Life BSP support
#define FTE_PEXT_MODELDBL           0x00001000  // Model index > 255
#define FTE_PEXT_ENTITYDBL          0x00002000  // Entity count > 512
#define FTE_PEXT_ENTITYDBL2         0x00004000  // Entity count > 1024
#define FTE_PEXT_FLOATCOORDS        0x00008000  // Float coordinates
#define FTE_PEXT_SPAWNSTATIC2       0x00400000  // Extended static entities
#define FTE_PEXT_COLOURMOD          0x00080000  // RGB color modification
#define FTE_PEXT_256PACKETENTITIES  0x01000000  // 256 packet entities
#define FTE_PEXT_CHUNKEDDOWNLOADS   0x20000000  // Chunked downloads
```

### MVD Protocol Extension Flags

```c
#define MVD_PEXT1_FLOATCOORDS       (1 << 0)  // Float coords for entities
#define MVD_PEXT1_HIGHLAGTELEPORT   (1 << 1)  // Teleport direction fix
#define MVD_PEXT1_HIDDEN_MESSAGES   (1 << 5)  // Hidden message support
```

---

## svc_playerinfo (42) - Player State Update

This is the core message type for player positions in MVD. The format differs between MVD and standard QWD.

### MVD Format

```
Offset  Size  Field
------  ----  -----
0       1     svc_playerinfo (42)
1       1     player_number (0-31)
2       2     flags (DF_* flags, little-endian short)
4       1     frame (animation frame)

// Conditional fields based on flags:
if (flags & DF_ORIGIN)      2  origin_x (coord)
if (flags & DF_ORIGIN<<1)   2  origin_y (coord)
if (flags & DF_ORIGIN<<2)   2  origin_z (coord)

if (flags & DF_ANGLES)      2  angle_pitch (angle16)
if (flags & DF_ANGLES<<1)   2  angle_yaw (angle16)
if (flags & DF_ANGLES<<2)   2  angle_roll (angle16)

if (flags & DF_MODEL)       1  model_index
if (flags & DF_SKINNUM)     1  skin_number
if (flags & DF_EFFECTS)     1  effects
if (flags & DF_WEAPONFRAME) 1  weapon_frame
```

### DF_* Flags (Delta Flags)

```c
#define DF_ORIGIN       (1 << 0)   // 0x0001 - Origin X present
#define DF_ORIGIN_Y     (1 << 1)   // 0x0002 - Origin Y present
#define DF_ORIGIN_Z     (1 << 2)   // 0x0004 - Origin Z present
#define DF_ANGLES       (1 << 3)   // 0x0008 - Angle pitch present
#define DF_ANGLES_Y     (1 << 4)   // 0x0010 - Angle yaw present
#define DF_ANGLES_Z     (1 << 5)   // 0x0020 - Angle roll present
#define DF_EFFECTS      (1 << 6)   // 0x0040 - Effects byte present
#define DF_SKINNUM      (1 << 7)   // 0x0080 - Skin number present
#define DF_DEAD         (1 << 8)   // 0x0100 - Player is dead
#define DF_GIB          (1 << 9)   // 0x0200 - Player is gibbed
#define DF_WEAPONFRAME  (1 << 10)  // 0x0400 - Weapon frame present
#define DF_MODEL        (1 << 11)  // 0x0800 - Model index present
```

### Model Index Extended Range

When `FTE_PEXT_MODELDBL` is enabled and both `DF_MODEL` and `DF_SKINNUM` are set:
- If `skin_number` has bit 7 set, add 256 to model_index
- Clear bit 7 from skin_number

```c
if ((flags & DF_MODEL) && (flags & DF_SKINNUM)) {
    if (skin_number & 0x80) {
        model_index += 256;
        skin_number &= 0x7F;
    }
}
```

---

## svc_updatestat (3) / svc_updatestatlong (38)

Player statistics update.

### svc_updatestat (byte value)
```
Offset  Size  Field
------  ----  -----
0       1     svc_updatestat (3)
1       1     stat_index
2       1     stat_value
```

### svc_updatestatlong (long value)
```
Offset  Size  Field
------  ----  -----
0       1     svc_updatestatlong (38)
1       1     stat_index
2       4     stat_value (little-endian long)
```

### Stat Indices

| Index | Name | Description |
|-------|------|-------------|
| 0 | `STAT_HEALTH` | Health points |
| 1 | `STAT_FRAGS` | Frag count |
| 2 | `STAT_WEAPON` | Current weapon model index |
| 3 | `STAT_AMMO` | Current ammo |
| 4 | `STAT_ARMOR` | Armor points |
| 5 | `STAT_WEAPONFRAME` | Weapon animation frame |
| 6 | `STAT_SHELLS` | Shell ammo count |
| 7 | `STAT_NAILS` | Nail ammo count |
| 8 | `STAT_ROCKETS` | Rocket ammo count |
| 9 | `STAT_CELLS` | Cell ammo count |
| 10 | `STAT_ACTIVEWEAPON` | Active weapon flags |
| 11 | `STAT_TOTALSECRETS` | Total secrets in level |
| 12 | `STAT_TOTALMONSTERS` | Total monsters in level |
| 13 | `STAT_SECRETS` | Secrets found |
| 14 | `STAT_MONSTERS` | Monsters killed |
| 15 | `STAT_ITEMS` | Item flags |
| 16 | `STAT_VIEWHEIGHT` | View height offset |
| 17 | `STAT_TIME` | Server time |

### Item Flags (STAT_ITEMS)

```c
#define IT_SHOTGUN              (1 << 0)
#define IT_SUPER_SHOTGUN        (1 << 1)
#define IT_NAILGUN              (1 << 2)
#define IT_SUPER_NAILGUN        (1 << 3)
#define IT_GRENADE_LAUNCHER     (1 << 4)
#define IT_ROCKET_LAUNCHER      (1 << 5)
#define IT_LIGHTNING            (1 << 6)
#define IT_SUPER_LIGHTNING      (1 << 7)
#define IT_SHELLS               (1 << 8)
#define IT_NAILS                (1 << 9)
#define IT_ROCKETS              (1 << 10)
#define IT_CELLS                (1 << 11)
#define IT_AXE                  (1 << 12)
#define IT_ARMOR1               (1 << 13)  // Green armor
#define IT_ARMOR2               (1 << 14)  // Yellow armor
#define IT_ARMOR3               (1 << 15)  // Red armor
#define IT_SUPERHEALTH          (1 << 16)
#define IT_KEY1                 (1 << 17)
#define IT_KEY2                 (1 << 18)
#define IT_INVISIBILITY         (1 << 19)
#define IT_INVULNERABILITY      (1 << 20)
#define IT_SUIT                 (1 << 21)
#define IT_QUAD                 (1 << 22)
// Bits 23-27 reserved
// Bits 28-31: Server flags (sigils)
```

---

## svc_updateuserinfo (40)

Player information update.

```
Offset  Size  Field
------  ----  -----
0       1     svc_updateuserinfo (40)
1       1     player_slot
2       4     user_id (little-endian long)
6       var   userinfo_string (null-terminated)
```

### Userinfo Keys

Common keys in the userinfo string:

| Key | Description |
|-----|-------------|
| `name` | Player name |
| `team` | Team name |
| `topcolor` | Top color (0-13) |
| `bottomcolor` | Bottom color (0-13) |
| `skin` | Skin name |
| `spectator` | "1" if spectator |
| `*client` | Client software name |

---

## svc_print (8)

Print message to console.

```
Offset  Size  Field
------  ----  -----
0       1     svc_print (8)
1       1     print_level
2       var   message (null-terminated string)
```

### Print Levels

| Value | Name | Description |
|-------|------|-------------|
| 0 | `PRINT_LOW` | Low priority |
| 1 | `PRINT_MEDIUM` | Medium priority |
| 2 | `PRINT_HIGH` | High priority |
| 3 | `PRINT_CHAT` | Chat message |

---

## Hidden Messages

When `MVD_PEXT1_HIDDEN_MESSAGES` is enabled, `dem_multiple` messages with `player_mask == 0` contain structured hidden data.

### Hidden Message Format

```
Offset  Size  Field
------  ----  -----
0       4     block_length (little-endian long)
4       2     type_id (little-endian short)
6       N     block_data (block_length bytes)
```

If `type_id == 0xFFFF`, read another short for extended type range.

### Hidden Message Types

| Type ID | Name | Description |
|---------|------|-------------|
| 0x0000 | `mvdhidden_antilag_position` | Antilag position data |
| 0x0001 | `mvdhidden_usercmd` | User command data |
| 0x0002 | `mvdhidden_usercmd_weapons` | Weapon selection data |
| 0x0003 | `mvdhidden_demoinfo` | Embedded demo info (JSON) |
| 0x0004 | `mvdhidden_commentary_track` | Commentary track info |
| 0x0005 | `mvdhidden_commentary_data` | Commentary audio data |
| 0x0006 | `mvdhidden_commentary_text_segment` | Commentary text |
| 0x0007 | `mvdhidden_dmgdone` | Damage dealt info |
| 0x0008 | `mvdhidden_usercmd_weapons_ss` | Server-side weapon data |
| 0x0009 | `mvdhidden_usercmd_weapon_instruction` | Weapon instruction |
| 0x000A | `mvdhidden_paused_duration` | Paused time (QTV only) |
| 0xFFFF | `mvdhidden_extended` | Extended type (read next short) |

### mvdhidden_antilag_position (0x0000)

```
// Header
Offset  Size  Field
------  ----  -----
0       1     player_num (shooting player)
1       1     num_players (number of position records)
2       4     incoming_sequence
6       4     server_time (float)
10      4     target_time (float)

// Per-player records (repeated num_players times)
0       12    client_position (3 floats: x, y, z)
12      12    antilag_position (3 floats: x, y, z)
24      1     player_num
25      1     msec
26      1     prediction_model
```

### mvdhidden_usercmd (0x0001)

```
Offset  Size  Field
------  ----  -----
0       1     player_num
1       1     drop_count
2       1     msec
3       12    angles (3 floats: pitch, yaw, roll)
15      2     forward_move (short)
17      2     side_move (short)
19      2     up_move (short)
21      1     buttons
22      1     impulse
```

### mvdhidden_demoinfo (0x0003)

```
Offset  Size  Field
------  ----  -----
0       2     block_number (short)
2       N     json_content (UTF-8 text)
```

### mvdhidden_dmgdone (0x0007)

```
Offset  Size  Field
------  ----  -----
0       1     type_flags (bit 15 = splash damage)
1       2     attacker_entity (short)
3       2     victim_entity (short)
5       2     damage_amount (short)
```

---

## Demo End Marker

The demo ends with a disconnect message:

```
Offset  Size  Field
------  ----  -----
0       1     time_delta (0)
1       1     message_type (dem_all = 6)
2       4     payload_size
6       1     svc_disconnect (2)
7       var   "EndOfDemo" (null-terminated string)
```

---

## Parsing Algorithm

### Pseudocode

```python
def parse_mvd(file):
    while not eof(file):
        # Read message header
        time_delta = read_byte(file)
        type_byte = read_byte(file)
        message_type = type_byte & 0x07

        if message_type == DEM_SET:  # 2
            incoming_seq = read_long(file)
            outgoing_seq = read_long(file)

        elif message_type == DEM_MULTIPLE:  # 3
            player_mask = read_long(file)
            size = read_long(file)
            payload = read_bytes(file, size)

            if player_mask == 0:
                parse_hidden_messages(payload)
            else:
                parse_network_message(payload)

        elif message_type == DEM_SINGLE:  # 4
            player_num = type_byte >> 3
            size = read_long(file)
            payload = read_bytes(file, size)
            parse_network_message(payload)

        elif message_type == DEM_STATS:  # 5
            player_num = type_byte >> 3
            size = read_long(file)
            payload = read_bytes(file, size)
            parse_stats_message(payload, player_num)

        elif message_type == DEM_ALL:  # 6
            size = read_long(file)
            payload = read_bytes(file, size)
            parse_network_message(payload)

        elif message_type == DEM_READ:  # 1
            size = read_long(file)
            payload = read_bytes(file, size)
            parse_network_message(payload)

def parse_network_message(data):
    while data.remaining():
        cmd = read_byte(data)

        if cmd == SVC_DISCONNECT:
            msg = read_string(data)
            if msg == "EndOfDemo":
                return END_OF_DEMO

        elif cmd == SVC_SERVERDATA:
            parse_serverdata(data)

        elif cmd == SVC_PLAYERINFO:
            parse_playerinfo_mvd(data)

        elif cmd == SVC_UPDATESTAT:
            stat = read_byte(data)
            value = read_byte(data)

        # ... handle other svc_* commands
```

---

## Example: Reading Player Positions

```python
def parse_playerinfo_mvd(data):
    player_num = read_byte(data)
    flags = read_short(data)
    frame = read_byte(data)

    origin = [None, None, None]
    angles = [None, None, None]

    # Read origin components
    for i in range(3):
        if flags & (DF_ORIGIN << i):
            origin[i] = read_coord(data)

    # Read angle components
    for i in range(3):
        if flags & (DF_ANGLES << i):
            angles[i] = read_angle16(data)

    model = None
    if flags & DF_MODEL:
        model = read_byte(data)

    skin = None
    if flags & DF_SKINNUM:
        skin = read_byte(data)
        # Handle extended model index
        if skin & 0x80 and model is not None:
            model += 256
            skin &= 0x7F

    effects = None
    if flags & DF_EFFECTS:
        effects = read_byte(data)

    weaponframe = None
    if flags & DF_WEAPONFRAME:
        weaponframe = read_byte(data)

    is_dead = bool(flags & DF_DEAD)
    is_gib = bool(flags & DF_GIB)

    return PlayerState(
        player_num=player_num,
        origin=origin,
        angles=angles,
        frame=frame,
        model=model,
        skin=skin,
        effects=effects,
        weaponframe=weaponframe,
        is_dead=is_dead,
        is_gib=is_gib
    )
```

---

## Constants Reference

### Message Types
```c
#define dem_cmd         0
#define dem_read        1
#define dem_set         2
#define dem_multiple    3
#define dem_single      4
#define dem_stats       5
#define dem_all         6
```

### Protocol Constants
```c
#define PROTOCOL_VERSION        28
#define MAX_CLIENTS             32
#define MAX_MODELS              512  // With extensions: 2048
#define MAX_SOUNDS              512
#define MAX_LIGHTSTYLES         64
#define MAX_CL_STATS            32
#define UPDATE_BACKUP           64
```

---

## Source Code References

For implementation details, refer to these files in the ezQuake source:

| File | Description |
|------|-------------|
| `src/sv_demo.c` | Server-side MVD recording |
| `src/cl_demo.c` | Client-side demo playback |
| `src/cl_ents.c` | Entity and player parsing |
| `src/cl_parse.c` | Network message parsing |
| `src/qwprot/src/protocol.h` | Protocol definitions |
| `src/server.h` | Server data structures |
| `src/client.h` | Client data structures |

---

## Version History

| Version | Changes |
|---------|---------|
| Original | Basic MVD format (MVDSV) |
| FTE Extensions | Model/entity doubling, float coords |
| MVD_PEXT1 | Hidden messages, antilag data |

---

## License

This documentation is based on analysis of the ezQuake source code, which is licensed under the GNU General Public License v2.
