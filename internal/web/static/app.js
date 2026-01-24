// MVD Analyzer Dashboard

let currentResult = null;

document.addEventListener('DOMContentLoaded', () => {
    setupFileUpload();
    setupTabs();
});

function setupFileUpload() {
    const dropZone = document.getElementById('drop-zone');
    const fileInput = document.getElementById('file-input');

    fileInput.addEventListener('change', (e) => {
        if (e.target.files.length > 0) {
            uploadFile(e.target.files[0]);
        }
    });

    dropZone.addEventListener('dragover', (e) => {
        e.preventDefault();
        dropZone.classList.add('dragover');
    });

    dropZone.addEventListener('dragleave', () => {
        dropZone.classList.remove('dragover');
    });

    dropZone.addEventListener('drop', (e) => {
        e.preventDefault();
        dropZone.classList.remove('dragover');
        if (e.dataTransfer.files.length > 0) {
            uploadFile(e.dataTransfer.files[0]);
        }
    });
}

function setupTabs() {
    const tabButtons = document.querySelectorAll('.tab-btn');
    tabButtons.forEach(btn => {
        btn.addEventListener('click', () => {
            const tabName = btn.dataset.tab;
            tabButtons.forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
            document.getElementById(`tab-${tabName}`).classList.add('active');
        });
    });
}

// Load demo from QuakeWorld Hub by game ID or URL
async function loadFromHub() {
    const input = document.getElementById('hub-input').value.trim();
    if (!input) {
        alert('Please enter a game ID or URL');
        return;
    }

    const status = document.getElementById('upload-status');
    const btn = document.getElementById('hub-load-btn');

    status.textContent = 'Fetching from QuakeWorld Hub...';
    status.className = 'status loading';
    btn.disabled = true;

    try {
        const response = await fetch('/api/hub/load', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({ input })
        });

        if (!response.ok) {
            throw new Error(await response.text());
        }

        const data = await response.json();
        status.textContent = 'Analysis complete!';
        status.className = 'status success';

        currentResult = data.result;

        // Store hub info for viewer links (before displayResults so Key Moments can use it)
        if (data.hub) {
            currentResult.hubInfo = data.hub;
        }

        displayResults(data.result);
    } catch (error) {
        status.textContent = 'Error: ' + error.message;
        status.className = 'status error';
    } finally {
        btn.disabled = false;
    }
}

async function uploadFile(file) {
    const status = document.getElementById('upload-status');
    status.textContent = 'Analyzing...';
    status.className = 'status loading';

    const formData = new FormData();
    formData.append('file', file);

    try {
        const response = await fetch('/api/analyze', {
            method: 'POST',
            body: formData
        });

        if (!response.ok) {
            throw new Error(await response.text());
        }

        const data = await response.json();
        status.textContent = 'Analysis complete!';
        status.className = 'status success';

        currentResult = data.result;
        displayResults(data.result);
    } catch (error) {
        status.textContent = 'Error: ' + error.message;
        status.className = 'status error';
    }
}

function displayResults(result) {
    // Reset timeline state before loading new demo
    resetTimelineState();

    document.getElementById('results-section').style.display = 'block';

    const demoInfo = result.demoInfo;

    // Match info from demoInfo
    if (demoInfo) {
        document.getElementById('map-name').textContent = demoInfo.map || result.match?.map || '-';
        document.getElementById('duration').textContent = formatDuration(demoInfo.duration || result.duration);
        document.getElementById('mode').textContent = demoInfo.mode || '-';
        document.getElementById('hostname').textContent = demoInfo.hostname || '-';
        document.getElementById('match-date').textContent = demoInfo.date || '-';
    } else if (result.match) {
        document.getElementById('map-name').textContent = result.match.map || '-';
        document.getElementById('duration').textContent = formatDuration(result.duration);
    }

    // Teams from demoInfo
    if (demoInfo && demoInfo.teams) {
        displayTeamsFromDemoInfo(demoInfo);
    } else if (result.match && result.match.teams) {
        displayTeams(result.match.teams);
    }

    // Player stats from demoInfo
    if (demoInfo && demoInfo.players) {
        displayPlayerStats(demoInfo.players);
        displayWeaponStatsTable(demoInfo.players);
        displayItemsTable(demoInfo.players);
        displayPerformanceTable(demoInfo.players);
    } else if (result.frags && result.frags.byPlayer) {
        displayScoreboardFallback(result.frags.byPlayer, result.match ? result.match.players : []);
    }

    // Weapons chart from frags
    if (result.frags && result.frags.byWeapon) {
        displayWeaponsChart(result.frags.byWeapon);
    }

    // Timeline Analysis (new graphical view)
    if (result.timelineAnalysis || result.messages?.events) {
        displayTimelineAnalysis(result);
    }

    // Key Moments (powerup runs)
    if (result.timelineAnalysis?.powerupEvents) {
        displayKeyMoments(result);
    }

    // Map View
    if (result.timelineAnalysis) {
        initMapView(result);
    }
}

function displayTeamsFromDemoInfo(demoInfo) {
    const container = document.getElementById('teams-list');
    container.innerHTML = '';

    // Calculate team scores from players
    const teamScores = {};
    for (const player of demoInfo.players || []) {
        const team = player.team || 'unknown';
        if (!teamScores[team]) {
            teamScores[team] = 0;
        }
        teamScores[team] += player.stats?.frags || 0;
    }

    // Sort by score
    const sorted = Object.entries(teamScores).sort((a, b) => b[1] - a[1]);

    sorted.forEach(([name, frags]) => {
        const div = document.createElement('div');
        div.className = 'team-item';
        div.innerHTML = `
            <span class="team-name">${escapeHtml(name)}</span>
            <span class="team-frags">${frags} frags</span>
        `;
        container.appendChild(div);
    });
}

function displayTeams(teams) {
    const container = document.getElementById('teams-list');
    container.innerHTML = '';

    const sorted = [...teams].sort((a, b) => b.frags - a.frags);

    sorted.forEach(team => {
        const div = document.createElement('div');
        div.className = 'team-item';
        div.innerHTML = `
            <span class="team-name">${escapeHtml(team.name)}</span>
            <span class="team-frags">${team.frags} frags</span>
        `;
        container.appendChild(div);
    });
}

function displayPlayerStats(players) {
    const tbody = document.getElementById('scoreboard-body');
    tbody.innerHTML = '';

    // Sort by frags
    const sorted = [...players].sort((a, b) => (b.stats?.frags || 0) - (a.stats?.frags || 0));

    sorted.forEach(player => {
        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td>${escapeHtml(player.name)}</td>
            <td>${escapeHtml(player.team || '')}</td>
            <td>${player.stats?.frags || 0}</td>
            <td>${player.stats?.deaths || 0}</td>
            <td>${player.stats?.tk || 0}</td>
            <td>${player.dmg?.given || 0}</td>
            <td>${player.dmg?.taken || 0}</td>
            <td>${player.ping || 0}</td>
        `;
        tbody.appendChild(tr);
    });
}

function displayWeaponStatsTable(players) {
    const tbody = document.getElementById('weapon-stats-body');
    tbody.innerHTML = '';

    const sorted = [...players].sort((a, b) => (b.dmg?.given || 0) - (a.dmg?.given || 0));

    sorted.forEach(player => {
        const w = player.weapons || {};
        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td>${escapeHtml(player.name)}</td>
            <td>${formatWeaponCell(w.sg)}</td>
            <td>${formatWeaponCell(w.ssg)}</td>
            <td>${formatWeaponCell(w.ng)}</td>
            <td>${formatWeaponCell(w.sng)}</td>
            <td>${formatWeaponCell(w.gl)}</td>
            <td>${formatWeaponCell(w.rl)}</td>
            <td>${formatWeaponCell(w.lg)}</td>
        `;
        tbody.appendChild(tr);
    });
}

function formatWeaponCell(weapon) {
    if (!weapon) return '-';

    const parts = [];

    // Accuracy
    if (weapon.acc && weapon.acc.attacks > 0) {
        const acc = ((weapon.acc.hits / weapon.acc.attacks) * 100).toFixed(1);
        parts.push(`<span class="${getAccuracyClass(parseFloat(acc))}">${acc}%</span>`);
    }

    // Kills
    const kills = weapon.kills?.total || weapon.kills?.enemy || 0;
    if (kills > 0) {
        parts.push(`<span class="weapon-kills">${kills}k</span>`);
    }

    // Damage
    const dmg = weapon.damage?.enemy || 0;
    if (dmg > 0) {
        parts.push(`<span class="weapon-dmg">${dmg}d</span>`);
    }

    return parts.length > 0 ? parts.join(' ') : '-';
}

function displayItemsTable(players) {
    const tbody = document.getElementById('items-body');
    tbody.innerHTML = '';

    const sorted = [...players].sort((a, b) => (b.stats?.frags || 0) - (a.stats?.frags || 0));

    sorted.forEach(player => {
        const items = player.items || {};
        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td>${escapeHtml(player.name)}</td>
            <td>${items.ra?.took || 0}</td>
            <td>${items.ya?.took || 0}</td>
            <td>${items.ga?.took || 0}</td>
            <td>${items.health_100?.took || 0}</td>
            <td>${formatPowerup(items.q)}</td>
            <td>${formatPowerup(items.p)}</td>
            <td>${formatPowerup(items.r)}</td>
        `;
        tbody.appendChild(tr);
    });
}

function formatPowerup(item) {
    if (!item || !item.took) return '0';
    if (item.time) {
        return `${item.took} (${item.time}s)`;
    }
    return `${item.took}`;
}

function displayPerformanceTable(players) {
    const tbody = document.getElementById('performance-body');
    tbody.innerHTML = '';

    const sorted = [...players].sort((a, b) => (b.stats?.frags || 0) - (a.stats?.frags || 0));

    sorted.forEach(player => {
        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td>${escapeHtml(player.name)}</td>
            <td>${player.spree?.max || 0}</td>
            <td>${player.spree?.quad || 0}</td>
            <td>${player.speed?.avg ? player.speed.avg.toFixed(0) : '-'}</td>
            <td>${player.speed?.max ? player.speed.max.toFixed(0) : '-'}</td>
            <td>${player.stats?.['spawn-frags'] || 0}</td>
        `;
        tbody.appendChild(tr);
    });
}

function displayScoreboardFallback(byPlayer, players) {
    const tbody = document.getElementById('scoreboard-body');
    tbody.innerHTML = '';

    const playerData = [];
    for (const [name, stats] of Object.entries(byPlayer)) {
        if (name.includes("'s quad") || name === 'teammate' || name === 'his teammate') {
            continue;
        }

        const playerInfo = players.find(p => p.name === name);
        playerData.push({
            name: name,
            team: playerInfo ? playerInfo.team : '',
            frags: playerInfo ? playerInfo.frags : (stats.kills - stats.deaths),
            deaths: stats.deaths,
            tk: 0,
            dmgGiven: 0,
            dmgTaken: 0,
            ping: 0
        });
    }

    playerData.sort((a, b) => b.frags - a.frags);

    playerData.forEach(player => {
        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td>${escapeHtml(player.name)}</td>
            <td>${escapeHtml(player.team)}</td>
            <td>${player.frags}</td>
            <td>${player.deaths}</td>
            <td>${player.tk}</td>
            <td>${player.dmgGiven}</td>
            <td>${player.dmgTaken}</td>
            <td>${player.ping}</td>
        `;
        tbody.appendChild(tr);
    });
}

function displayWeaponsChart(byWeapon) {
    const container = document.getElementById('weapons-chart');
    container.innerHTML = '';

    const sorted = Object.entries(byWeapon).sort((a, b) => b[1] - a[1]);
    const max = sorted.length > 0 ? sorted[0][1] : 1;

    sorted.forEach(([weapon, count]) => {
        const div = document.createElement('div');
        div.className = 'weapon-bar';
        const percentage = (count / max) * 100;
        div.innerHTML = `
            <span class="weapon-name">${getWeaponName(weapon)}</span>
            <div class="bar-container">
                <div class="bar" style="width: ${percentage}%"></div>
            </div>
            <span class="weapon-count">${count}</span>
        `;
        container.appendChild(div);
    });
}

function displayTimeline(events, teams) {
    const container = document.getElementById('timeline-body');
    container.innerHTML = '';

    const teamNames = Array.isArray(teams) ? teams : teams.map(t => t.name);
    if (teamNames.length >= 2) {
        document.getElementById('team1-header').textContent = teamNames[0];
        document.getElementById('team2-header').textContent = teamNames[1];
    }

    const relevantEvents = events.filter(e =>
        e.type === 'frag' || e.type === 'chat' || e.type === 'teamsay'
    ).slice(0, 500);

    relevantEvents.forEach(event => {
        const row = document.createElement('div');
        row.className = 'timeline-row';

        const isTeam1 = teamNames.length >= 1 && event.team === teamNames[0];
        const isTeam2 = teamNames.length >= 2 && event.team === teamNames[1];

        const eventHtml = `<span class="timeline-event ${event.type}">${escapeHtml(event.message)}</span>`;

        row.innerHTML = `
            <div class="timeline-left">${isTeam1 ? eventHtml : ''}</div>
            <div class="timeline-time">${formatTime(event.time)}</div>
            <div class="timeline-right">${isTeam2 ? eventHtml : ''}</div>
        `;

        container.appendChild(row);
    });
}

function getAccuracyClass(acc) {
    if (acc >= 40) return 'accuracy-high';
    if (acc >= 25) return 'accuracy-medium';
    return 'accuracy-low';
}

function displayKeyMoments(result) {
    const tbody = document.getElementById('keymoments-body');
    const emptyMsg = document.getElementById('keymoments-empty');
    tbody.innerHTML = '';

    const powerupEvents = result.timelineAnalysis?.powerupEvents || [];
    const matchStartTime = result.timelineAnalysis?.matchStartTime || 0;

    if (powerupEvents.length === 0) {
        emptyMsg.style.display = 'block';
        return;
    }
    emptyMsg.style.display = 'none';

    // Get hub info for viewer links (from currentResult which may have hubInfo set)
    const hubInfo = currentResult?.hubInfo;

    powerupEvents.forEach(event => {
        const tr = document.createElement('tr');

        // Calculate match-relative time for display
        const relTime = Math.max(0, event.time - matchStartTime);

        // Build viewer URL if hub info available
        let watchCell = '-';
        if (hubInfo && hubInfo.gameId) {
            // Use raw demo time for Hub viewer (includes countdown period)
            // Demo time = event.time (already in demo time, not match-relative)
            const fromTime = Math.max(0, Math.floor(event.time) - 10);
            const toTime = Math.floor(event.endTime) + 5;

            // Use playerUserID from MVD demo data - this is what Hub viewer expects for track param
            // Falls back to playerSlot if UserID is 0 (shouldn't happen in well-formed demos)
            const trackId = event.playerUserID || event.playerSlot;

            const viewerUrl = `https://hub.quakeworld.nu/games/?gameId=${hubInfo.gameId}&from=${fromTime}&to=${toTime}&track=${trackId}`;
            watchCell = `<a href="${viewerUrl}" target="_blank" class="viewer-link">Watch</a>`;
        }

        // Powerup display with color
        const powerupDisplay = getPowerupDisplay(event.powerupType);

        tr.innerHTML = `
            <td class="time-cell">${formatTime(relTime)}</td>
            <td class="powerup-cell ${event.powerupType}">${powerupDisplay}</td>
            <td>${escapeHtml(event.playerName || 'Unknown')}</td>
            <td>${escapeHtml(event.team || '-')}</td>
            <td>${Math.round(event.duration)}s</td>
            <td>${watchCell}</td>
        `;
        tbody.appendChild(tr);
    });
}

function getPowerupDisplay(type) {
    switch(type) {
        case 'quad': return 'Quad';
        case 'pent': return 'Pent';
        case 'ring': return 'Ring';
        default: return type;
    }
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

function formatDuration(seconds) {
    const mins = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    return `${mins}:${secs.toString().padStart(2, '0')}`;
}

function formatTime(seconds) {
    return formatDuration(seconds);
}

function getWeaponName(code) {
    const names = {
        'rl': 'Rocket Launcher',
        'lg': 'Lightning Gun',
        'gl': 'Grenade Launcher',
        'ssg': 'Super Shotgun',
        'sg': 'Shotgun',
        'sng': 'Super Nailgun',
        'ng': 'Nailgun',
        'axe': 'Axe',
        'tele': 'Telefrag',
        'suicide': 'Suicide',
        'teamkill': 'Team Kill',
        'fall': 'Fall',
        'water': 'Drowning',
        'lava': 'Lava',
        'slime': 'Slime'
    };
    return names[code] || code;
}

function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// Escape a single character for HTML
function escapeHtmlChar(char) {
    switch (char) {
        case '<': return '&lt;';
        case '>': return '&gt;';
        case '&': return '&amp;';
        case '"': return '&quot;';
        case "'": return '&#39;';
        default: return char;
    }
}

// Format Quake chat messages with colors
// Based on ezQuake source code character rendering:
// - Characters 0-127: Normal white text
// - Characters 128-255: "Gold/Brown" alternate text (same glyphs as 0-127)
// - &cRGB: Set color to RGB (hex digits 0-F, each multiplied by 16)
// - &r: Reset color to white
function formatQuakeMessage(text) {
    if (!text) return '';

    // Remove sound triggers at end of messages (!K, !H, !G, !C, etc.)
    let result = text.replace(/![A-Z]$/g, '');

    let output = '';
    let currentColor = null;
    let i = 0;

    while (i < result.length) {
        const charCode = result.charCodeAt(i);

        // Check for &cRGB color code
        if (result.slice(i, i + 2) === '&c') {
            // Check for &cfff (white/reset)
            if (result.slice(i, i + 5).toLowerCase() === '&cfff') {
                if (currentColor) output += '</span>';
                currentColor = null;
                i += 5;
                continue;
            }
            // Check for 3-digit hex color
            const colorMatch = result.slice(i + 2, i + 5).match(/^[0-9a-fA-F]{3}/);
            if (colorMatch) {
                if (currentColor) output += '</span>';
                // ezQuake uses r * 16 for each hex digit (0-240 range)
                const r = parseInt(colorMatch[0][0], 16) * 16;
                const g = parseInt(colorMatch[0][1], 16) * 16;
                const b = parseInt(colorMatch[0][2], 16) * 16;
                currentColor = `rgb(${r},${g},${b})`;
                output += `<span style="color:${currentColor}">`;
                i += 5;
                continue;
            }
        }

        // Check for &r reset
        if (result.slice(i, i + 2) === '&r') {
            if (currentColor) output += '</span>';
            currentColor = null;
            i += 2;
            continue;
        }

        // High-bit gold characters (128-255)
        if (charCode >= 128 && charCode <= 255) {
            const baseChar = String.fromCharCode(charCode - 128);
            if (currentColor === null) {
                output += `<span class="quake-gold">${escapeHtmlChar(baseChar)}</span>`;
            } else {
                output += escapeHtmlChar(baseChar);
            }
            i++;
            continue;
        }

        // Skip macro delimiters (curly braces and square brackets)
        // These are Quake client markup, not displayed text
        if (result[i] === '{' || result[i] === '}' ||
            result[i] === '[' || result[i] === ']') {
            i++;
            continue;
        }

        // Regular character
        output += escapeHtmlChar(result[i]);
        i++;
    }

    if (currentColor) output += '</span>';
    return output;
}

// Timeline Analysis State
let timelineState = {
    buckets: [],
    events: [],
    duration: 0,
    matchStartTime: 0,
    teams: [],
    selection: { start: 0, end: 60 },
    brushing: false,
    brushMode: null,
    dragStartX: 0,
    dragStartSelection: null,
    overviewBucketSize: 5, // Aggregate to 5-second buckets for overview
    brushInitialized: false // Track if brush handlers are set up
};

// Reset all timeline state for loading a new demo
function resetTimelineState() {
    timelineState.buckets = [];
    timelineState.events = [];
    timelineState.fragEvents = [];
    timelineState.duration = 0;
    timelineState.matchStartTime = 0;
    timelineState.teams = [];
    timelineState.selection = { start: 0, end: 60 };
    timelineState.brushing = false;
    timelineState.brushMode = null;
    timelineState.dragStartX = 0;
    timelineState.dragStartSelection = null;

    // Clear all timeline graph containers
    const containers = [
        'overview-graph', 'overview-axis', 'detail-graph', 'detail-axis',
        'powerup-lines-top', 'powerup-lines-bottom',
        'health-armor-graph', 'health-axis', 'frags-graph', 'frags-axis',
        'score-graph', 'score-axis', 'kill-messages', 'team-a-messages', 'team-b-messages'
    ];
    containers.forEach(id => {
        const el = document.getElementById(id);
        if (el) el.innerHTML = '';
    });
}

function displayTimelineAnalysis(result) {
    const timeline = result.timelineAnalysis;
    const demoInfo = result.demoInfo;

    // Get teams
    let teams = [];
    if (demoInfo?.teams) {
        teams = demoInfo.teams;
    } else if (result.match?.teams) {
        teams = result.match.teams.map(t => t.name);
    }

    timelineState.buckets = timeline?.buckets || [];
    timelineState.matchStartTime = timeline?.matchStartTime || 0;
    timelineState.duration = result.duration || 600;
    timelineState.teams = teams;
    timelineState.events = result.messages?.events || [];
    timelineState.fragEvents = timeline?.fragEvents || []; // Frag events from stat tracking

    // Start selection at match start
    const effectiveStart = timelineState.matchStartTime;
    timelineState.selection = {
        start: effectiveStart,
        end: Math.min(effectiveStart + 60, timelineState.duration)
    };

    // Update legend team names
    if (teams.length >= 2) {
        document.getElementById('legend-team-a').textContent = teams[0] + ' ↑';
        document.getElementById('legend-team-b').textContent = teams[1] + ' ↓';
        document.getElementById('team-a-chat-title').textContent = `${teams[0]} Chat`;
        document.getElementById('team-b-chat-title').textContent = `${teams[1]} Chat`;
        document.getElementById('legend-health-team-a').textContent = teams[0] + ' ↑';
        document.getElementById('legend-health-team-b').textContent = teams[1] + ' ↓';
    }

    renderOverviewGraph();
    renderOverviewAxis();
    setupBrush();
    updateDetailView();
}

function renderOverviewGraph() {
    const container = document.getElementById('overview-graph');
    container.innerHTML = '';

    const buckets = timelineState.buckets;
    const teams = timelineState.teams;
    const matchStart = timelineState.matchStartTime;

    if (!buckets || buckets.length === 0 || teams.length < 2) return;

    // Filter to match time only (after warmup)
    const matchBuckets = buckets.filter(b => b.startTime >= matchStart);

    // Aggregate to 5-second buckets for overview
    const aggregated = aggregateBuckets(matchBuckets, timelineState.overviewBucketSize, teams);

    // Find max value for scaling (use 4 as typical max for 4v4)
    let maxTeamValue = 4;
    for (const bucket of aggregated) {
        const teamATotal = bucket.teamA.rl + bucket.teamA.lg + bucket.teamA.rllg +
                          bucket.teamA.quad + bucket.teamA.pent + bucket.teamA.ring;
        const teamBTotal = bucket.teamB.rl + bucket.teamB.lg + bucket.teamB.rllg +
                          bucket.teamB.quad + bucket.teamB.pent + bucket.teamB.ring;
        maxTeamValue = Math.max(maxTeamValue, teamATotal, teamBTotal);
    }

    // Update Y-axis labels
    document.querySelector('#overview-y-axis .y-top').textContent = maxTeamValue;
    document.querySelector('#overview-y-axis .y-bottom').textContent = maxTeamValue;

    const barHeight = 40; // pixels for max value

    // Create diverging bars (Team A up, Team B down)
    for (const bucket of aggregated) {
        const bar = document.createElement('div');
        bar.className = 'diverging-bar';

        // Team A goes up (above center axis) - weapons closer to axis
        const topContainer = document.createElement('div');
        topContainer.className = 'diverging-bar-top';
        addGranularSegments(topContainer, bucket.teamA, maxTeamValue, barHeight);

        // Team B goes down (below center axis)
        const bottomContainer = document.createElement('div');
        bottomContainer.className = 'diverging-bar-bottom';
        addGranularSegments(bottomContainer, bucket.teamB, maxTeamValue, barHeight);

        bar.appendChild(topContainer);
        bar.appendChild(bottomContainer);
        container.appendChild(bar);
    }
}

// Calculate optimal bin size based on selection duration
function getOptimalBinSize(selectionDuration) {
    if (selectionDuration <= 300) return 1;   // ≤5min: 1s bins
    if (selectionDuration <= 600) return 2;   // ≤10min: 2s bins
    if (selectionDuration <= 900) return 3;   // ≤15min: 3s bins
    if (selectionDuration <= 1200) return 4;  // ≤20min: 4s bins
    return 5;                                  // >20min: 5s bins
}

// Aggregate 1-second detail buckets into larger bins for graphs
function aggregateDetailBuckets(buckets, binSize, teams) {
    if (buckets.length === 0 || binSize <= 1) return buckets;

    const result = [];
    const startTime = buckets[0].startTime;
    const endTime = buckets[buckets.length - 1].endTime;

    for (let t = startTime; t < endTime; t += binSize) {
        const binBuckets = buckets.filter(b => b.startTime >= t && b.startTime < t + binSize);
        if (binBuckets.length === 0) continue;

        // Aggregate team data using max for player counts, average for health/armor
        const aggregated = {
            startTime: t,
            endTime: t + binSize,
            teamData: {}
        };

        for (const team of teams) {
            const teamBuckets = binBuckets.map(b => (b.teamData || {})[team] || {});

            // Aggregate armorByType - use max for each armor type count
            const armorByType = {};
            for (const tb of teamBuckets) {
                const abt = tb.armorByType || {};
                for (const armorType of ['ra', 'ya', 'ga']) {
                    if (abt[armorType]) {
                        armorByType[armorType] = Math.max(armorByType[armorType] || 0, abt[armorType]);
                    }
                }
            }

            aggregated.teamData[team] = {
                // Use max within bin for player counts (shows peak control)
                playersWithRL: Math.max(...teamBuckets.map(tb => tb.playersWithRL || 0)),
                playersWithLG: Math.max(...teamBuckets.map(tb => tb.playersWithLG || 0)),
                playersWithRLLG: Math.max(...teamBuckets.map(tb => tb.playersWithRLLG || 0)),
                playersWithQuad: Math.max(...teamBuckets.map(tb => tb.playersWithQuad || 0)),
                playersWithPent: Math.max(...teamBuckets.map(tb => tb.playersWithPent || 0)),
                playersWithRing: Math.max(...teamBuckets.map(tb => tb.playersWithRing || 0)),
                // Use average for health/armor totals
                totalHealth: Math.round(teamBuckets.reduce((sum, tb) => sum + (tb.totalHealth || 0), 0) / teamBuckets.length),
                totalArmor: Math.round(teamBuckets.reduce((sum, tb) => sum + (tb.totalArmor || 0), 0) / teamBuckets.length),
                // Preserve armor type breakdown
                armorByType: armorByType
            };
        }

        result.push(aggregated);
    }

    return result;
}

// Aggregate 1-second buckets into larger time windows (for overview)
function aggregateBuckets(buckets, windowSize, teams) {
    if (buckets.length === 0) return [];

    const result = [];
    let currentWindow = null;
    let windowStart = buckets[0].startTime;

    for (const bucket of buckets) {
        // Start new window if needed
        if (!currentWindow || bucket.startTime >= windowStart + windowSize) {
            if (currentWindow) result.push(currentWindow);
            windowStart = Math.floor(bucket.startTime / windowSize) * windowSize;
            currentWindow = {
                startTime: windowStart,
                endTime: windowStart + windowSize,
                teamA: { rl: 0, lg: 0, rllg: 0, quad: 0, pent: 0, ring: 0, health: 0, armor: 0, count: 0 },
                teamB: { rl: 0, lg: 0, rllg: 0, quad: 0, pent: 0, ring: 0, health: 0, armor: 0, count: 0 }
            };
        }

        // Aggregate data
        const td = bucket.teamData || {};
        const teamAData = td[teams[0]] || {};
        const teamBData = td[teams[1]] || {};

        // Use max within window (not sum) for player counts
        currentWindow.teamA.rl = Math.max(currentWindow.teamA.rl, teamAData.playersWithRL || 0);
        currentWindow.teamA.lg = Math.max(currentWindow.teamA.lg, teamAData.playersWithLG || 0);
        currentWindow.teamA.rllg = Math.max(currentWindow.teamA.rllg, teamAData.playersWithRLLG || 0);
        currentWindow.teamA.quad = Math.max(currentWindow.teamA.quad, teamAData.playersWithQuad || 0);
        currentWindow.teamA.pent = Math.max(currentWindow.teamA.pent, teamAData.playersWithPent || 0);
        currentWindow.teamA.ring = Math.max(currentWindow.teamA.ring, teamAData.playersWithRing || 0);
        currentWindow.teamA.health += teamAData.totalHealth || 0;
        currentWindow.teamA.armor += teamAData.totalArmor || 0;
        currentWindow.teamA.count++;

        currentWindow.teamB.rl = Math.max(currentWindow.teamB.rl, teamBData.playersWithRL || 0);
        currentWindow.teamB.lg = Math.max(currentWindow.teamB.lg, teamBData.playersWithLG || 0);
        currentWindow.teamB.rllg = Math.max(currentWindow.teamB.rllg, teamBData.playersWithRLLG || 0);
        currentWindow.teamB.quad = Math.max(currentWindow.teamB.quad, teamBData.playersWithQuad || 0);
        currentWindow.teamB.pent = Math.max(currentWindow.teamB.pent, teamBData.playersWithPent || 0);
        currentWindow.teamB.ring = Math.max(currentWindow.teamB.ring, teamBData.playersWithRing || 0);
        currentWindow.teamB.health += teamBData.totalHealth || 0;
        currentWindow.teamB.armor += teamBData.totalArmor || 0;
        currentWindow.teamB.count++;
    }

    if (currentWindow) result.push(currentWindow);
    return result;
}

// Add granular weapon/powerup segments to a container
function addGranularSegments(container, data, maxValue, maxHeight) {
    // Order: weapons (RL, LG, RL+LG) closest to axis, then powerups (Quad, Pent, Ring)
    const segments = [
        { value: data.rl, className: 'rl' },
        { value: data.lg, className: 'lg' },
        { value: data.rllg, className: 'rllg' },
        { value: data.quad, className: 'quad' },
        { value: data.pent, className: 'pent' },
        { value: data.ring, className: 'ring' }
    ];

    for (const seg of segments) {
        if (seg.value > 0) {
            const el = document.createElement('div');
            el.className = `bar-segment ${seg.className}`;
            el.style.height = `${(seg.value / maxValue) * maxHeight}px`;
            container.appendChild(el);
        }
    }
}

function renderOverviewAxis() {
    const container = document.getElementById('overview-axis');
    container.innerHTML = '';

    const matchStart = timelineState.matchStartTime;
    const duration = timelineState.duration - matchStart;
    const tickCount = 5;

    for (let i = 0; i <= tickCount; i++) {
        const time = (duration / tickCount) * i;
        const span = document.createElement('span');
        span.textContent = formatTime(time);
        container.appendChild(span);
    }
}

function setupBrush() {
    // Only set up event handlers once - they reference timelineState which updates
    if (timelineState.brushInitialized) {
        updateBrushPosition();
        return;
    }

    const container = document.getElementById('timeline-overview-container');
    const selection = document.getElementById('brush-selection');
    const leftHandle = document.getElementById('brush-handle-left');
    const rightHandle = document.getElementById('brush-handle-right');

    updateBrushPosition();

    // Mouse event handlers - these reference timelineState dynamically
    function startDrag(e, mode) {
        e.preventDefault();
        timelineState.brushing = true;
        timelineState.brushMode = mode;
        timelineState.dragStartX = e.clientX;
        timelineState.dragStartSelection = { ...timelineState.selection };

        document.addEventListener('mousemove', onDrag);
        document.addEventListener('mouseup', endDrag);
    }

    function onDrag(e) {
        if (!timelineState.brushing) return;

        const containerRect = container.getBoundingClientRect();
        const containerWidth = containerRect.width - 20; // Account for padding
        const currentX = e.clientX - containerRect.left - 10;
        const currentTime = (currentX / containerWidth) * timelineState.duration;

        const minWindow = 10; // Minimum 10 second window

        switch (timelineState.brushMode) {
            case 'move':
                const deltaX = e.clientX - timelineState.dragStartX;
                const deltaTime = (deltaX / containerWidth) * timelineState.duration;
                let newStart = timelineState.dragStartSelection.start + deltaTime;
                let newEnd = timelineState.dragStartSelection.end + deltaTime;

                // Clamp to bounds
                if (newStart < 0) {
                    newEnd -= newStart;
                    newStart = 0;
                }
                if (newEnd > timelineState.duration) {
                    newStart -= (newEnd - timelineState.duration);
                    newEnd = timelineState.duration;
                }

                timelineState.selection.start = Math.max(0, newStart);
                timelineState.selection.end = Math.min(timelineState.duration, newEnd);
                break;

            case 'resize-left':
                timelineState.selection.start = Math.max(0,
                    Math.min(currentTime, timelineState.selection.end - minWindow));
                break;

            case 'resize-right':
                timelineState.selection.end = Math.min(timelineState.duration,
                    Math.max(currentTime, timelineState.selection.start + minWindow));
                break;
        }

        updateBrushPosition();
        updateDetailView();
    }

    function endDrag() {
        timelineState.brushing = false;
        timelineState.brushMode = null;
        document.removeEventListener('mousemove', onDrag);
        document.removeEventListener('mouseup', endDrag);
    }

    // Attach event listeners
    selection.addEventListener('mousedown', (e) => startDrag(e, 'move'));
    leftHandle.addEventListener('mousedown', (e) => startDrag(e, 'resize-left'));
    rightHandle.addEventListener('mousedown', (e) => startDrag(e, 'resize-right'));

    // Click on container to jump selection
    container.addEventListener('click', (e) => {
        if (timelineState.brushing) return;
        if (e.target.closest('.brush-selection, .brush-handle')) return;

        const containerRect = container.getBoundingClientRect();
        const containerWidth = containerRect.width - 20;
        const clickX = e.clientX - containerRect.left - 10;
        const clickTime = (clickX / containerWidth) * timelineState.duration;

        const windowSize = timelineState.selection.end - timelineState.selection.start;
        const halfWindow = windowSize / 2;

        timelineState.selection.start = Math.max(0, clickTime - halfWindow);
        timelineState.selection.end = Math.min(timelineState.duration, clickTime + halfWindow);

        // Adjust if we hit boundaries
        if (timelineState.selection.start === 0) {
            timelineState.selection.end = Math.min(timelineState.duration, windowSize);
        }
        if (timelineState.selection.end === timelineState.duration) {
            timelineState.selection.start = Math.max(0, timelineState.duration - windowSize);
        }

        updateBrushPosition();
        updateDetailView();
    });

    timelineState.brushInitialized = true;
}

function updateBrushPosition() {
    const selection = document.getElementById('brush-selection');
    const leftHandle = document.getElementById('brush-handle-left');
    const rightHandle = document.getElementById('brush-handle-right');

    const startPct = (timelineState.selection.start / timelineState.duration) * 100;
    const endPct = (timelineState.selection.end / timelineState.duration) * 100;
    const widthPct = endPct - startPct;

    selection.style.left = `${startPct}%`;
    selection.style.width = `${widthPct}%`;

    leftHandle.style.left = `${startPct}%`;
    rightHandle.style.left = `${endPct}%`;
}

function updateDetailView() {
    const { start, end } = timelineState.selection;
    const matchStart = timelineState.matchStartTime;

    // Update time range label (relative to match start)
    const relStart = start - matchStart;
    const relEnd = end - matchStart;
    document.getElementById('time-range-label').textContent =
        `(${formatTime(Math.max(0, relStart))} - ${formatTime(Math.max(0, relEnd))})`;

    // Update all detail panels
    updateDetailMessages(start, end);
    updateDetailGraph(start, end);
    updateDetailAxis(start, end);
    updateHealthArmorGraph(start, end);
    updateFragsGraph(start, end);
    updateScoreTimeline(start, end);
}

function updateDetailMessages(startTime, endTime) {
    const killContainer = document.getElementById('kill-messages');
    const teamAContainer = document.getElementById('team-a-messages');
    const teamBContainer = document.getElementById('team-b-messages');

    killContainer.innerHTML = '';
    teamAContainer.innerHTML = '';
    teamBContainer.innerHTML = '';

    if (!currentResult?.messages?.events) {
        const emptyMsg = '<div style="color: #888; padding: 20px; text-align: center;">No events</div>';
        killContainer.innerHTML = emptyMsg;
        teamAContainer.innerHTML = emptyMsg;
        teamBContainer.innerHTML = emptyMsg;
        return;
    }

    const teams = timelineState.teams;
    const matchStart = timelineState.matchStartTime;

    // Filter events in time range
    const events = currentResult.messages.events.filter(e =>
        e.time >= startTime && e.time <= endTime
    );

    // Deduplicate (same message within 1 second)
    const seen = new Map();
    const deduped = events.filter(e => {
        const key = `${Math.floor(e.time)}:${e.message}`;
        if (seen.has(key)) return false;
        seen.set(key, true);
        return true;
    });

    let killCount = 0, teamACount = 0, teamBCount = 0;

    // Sort into three categories
    deduped.forEach(event => {
        const relTime = event.time - matchStart;
        const item = document.createElement('div');
        item.className = 'timeline-message-item';
        item.innerHTML = `
            <span class="timeline-message-time">${formatTime(Math.max(0, relTime))}</span>
            <span class="timeline-message-content ${event.type}">${formatQuakeMessage(event.message)}</span>
        `;

        if (event.type === 'frag') {
            if (killCount < 100) {
                killContainer.appendChild(item);
                killCount++;
            }
        } else if (event.type === 'teamsay' || event.type === 'chat') {
            if (teams.length >= 2 && event.team === teams[0]) {
                if (teamACount < 50) {
                    teamAContainer.appendChild(item);
                    teamACount++;
                }
            } else if (teams.length >= 2 && event.team === teams[1]) {
                if (teamBCount < 50) {
                    teamBContainer.appendChild(item);
                    teamBCount++;
                }
            }
        }
    });

    if (killCount === 0) {
        killContainer.innerHTML = '<div style="color: #888; padding: 10px; text-align: center;">No kills</div>';
    }
    if (teamACount === 0) {
        teamAContainer.innerHTML = '<div style="color: #888; padding: 10px; text-align: center;">No messages</div>';
    }
    if (teamBCount === 0) {
        teamBContainer.innerHTML = '<div style="color: #888; padding: 10px; text-align: center;">No messages</div>';
    }
}

function updateDetailGraph(startTime, endTime) {
    const container = document.getElementById('detail-graph');
    container.innerHTML = '';

    const buckets = timelineState.buckets;
    const teams = timelineState.teams;

    if (!buckets || buckets.length === 0 || teams.length < 2) return;

    // Filter buckets within range (1-second resolution)
    const filteredBuckets = buckets.filter(b =>
        b.startTime >= startTime && b.endTime <= endTime
    );

    if (filteredBuckets.length === 0) return;

    // Apply dynamic binning based on selection duration
    const selectionDuration = endTime - startTime;
    const binSize = getOptimalBinSize(selectionDuration);
    const displayBuckets = aggregateDetailBuckets(filteredBuckets, binSize, teams);

    // Find max value for scaling (weapons only, use 4 as typical max for 4v4)
    let maxTeamValue = 4;
    for (const bucket of displayBuckets) {
        const td = bucket.teamData || {};
        const teamA = td[teams[0]] || {};
        const teamB = td[teams[1]] || {};
        // Only count weapons, not powerups
        const teamATotal = (teamA.playersWithRL || 0) + (teamA.playersWithLG || 0) + (teamA.playersWithRLLG || 0);
        const teamBTotal = (teamB.playersWithRL || 0) + (teamB.playersWithLG || 0) + (teamB.playersWithRLLG || 0);
        maxTeamValue = Math.max(maxTeamValue, teamATotal, teamBTotal);
    }

    // Update Y-axis labels
    document.querySelector('#detail-y-axis .y-top').textContent = maxTeamValue;
    document.querySelector('#detail-y-axis .y-bottom').textContent = maxTeamValue;

    const barHeight = 90; // pixels for max value

    // Create diverging bars (Team A up, Team B down) - weapons only
    for (const bucket of displayBuckets) {
        const bar = document.createElement('div');
        bar.className = 'diverging-bar';

        const td = bucket.teamData || {};
        const teamAData = td[teams[0]] || {};
        const teamBData = td[teams[1]] || {};

        // Build team data objects (weapons only)
        const teamA = {
            rl: teamAData.playersWithRL || 0,
            lg: teamAData.playersWithLG || 0,
            rllg: teamAData.playersWithRLLG || 0
        };
        const teamB = {
            rl: teamBData.playersWithRL || 0,
            lg: teamBData.playersWithLG || 0,
            rllg: teamBData.playersWithRLLG || 0
        };

        // Team A goes up (above center axis)
        const topContainer = document.createElement('div');
        topContainer.className = 'diverging-bar-top';
        addWeaponSegments(topContainer, teamA, maxTeamValue, barHeight);

        // Team B goes down (below center axis)
        const bottomContainer = document.createElement('div');
        bottomContainer.className = 'diverging-bar-bottom';
        addWeaponSegments(bottomContainer, teamB, maxTeamValue, barHeight);

        bar.appendChild(topContainer);
        bar.appendChild(bottomContainer);
        container.appendChild(bar);
    }

    // Render powerup lines separately
    updatePowerupLines(filteredBuckets, startTime, endTime, teams);
}

// Add weapon segments only (no powerups)
function addWeaponSegments(container, data, maxValue, maxHeight) {
    const segments = [
        { value: data.rl, className: 'rl' },
        { value: data.lg, className: 'lg' },
        { value: data.rllg, className: 'rllg' }
    ];

    for (const seg of segments) {
        if (seg.value > 0) {
            const el = document.createElement('div');
            el.className = `bar-segment ${seg.className}`;
            el.style.height = `${(seg.value / maxValue) * maxHeight}px`;
            container.appendChild(el);
        }
    }
}

// Render powerup lines as horizontal spans showing duration
function updatePowerupLines(buckets, startTime, endTime, teams) {
    const topContainer = document.getElementById('powerup-lines-top');
    const bottomContainer = document.getElementById('powerup-lines-bottom');
    topContainer.innerHTML = '';
    bottomContainer.innerHTML = '';

    if (buckets.length === 0 || teams.length < 2) return;

    const duration = endTime - startTime;
    const powerupTypes = ['quad', 'pent', 'ring'];

    // For each team, find contiguous spans where powerup is active
    for (let teamIdx = 0; teamIdx < 2; teamIdx++) {
        const team = teams[teamIdx];
        const container = teamIdx === 0 ? topContainer : bottomContainer;

        for (const powerup of powerupTypes) {
            const field = `playersWith${powerup.charAt(0).toUpperCase() + powerup.slice(1)}`;

            // Find spans where this powerup is active
            let spanStart = null;
            for (let i = 0; i < buckets.length; i++) {
                const bucket = buckets[i];
                const td = bucket.teamData || {};
                const teamData = td[team] || {};
                const hasIt = (teamData[field] || 0) > 0;

                if (hasIt && spanStart === null) {
                    spanStart = bucket.startTime;
                } else if (!hasIt && spanStart !== null) {
                    // End of span
                    addPowerupLine(container, spanStart, bucket.startTime, startTime, duration, powerup);
                    spanStart = null;
                }
            }
            // Handle span that extends to end
            if (spanStart !== null) {
                addPowerupLine(container, spanStart, endTime, startTime, duration, powerup);
            }
        }
    }
}

function addPowerupLine(container, spanStart, spanEnd, viewStart, viewDuration, powerupType) {
    const leftPct = ((spanStart - viewStart) / viewDuration) * 100;
    const widthPct = ((spanEnd - spanStart) / viewDuration) * 100;

    if (widthPct > 0) {
        const line = document.createElement('div');
        line.className = `powerup-line ${powerupType}`;
        line.style.left = `${leftPct}%`;
        line.style.width = `${widthPct}%`;
        container.appendChild(line);
    }
}

function updateDetailAxis(startTime, endTime) {
    const container = document.getElementById('detail-axis');
    container.innerHTML = '';

    const matchStart = timelineState.matchStartTime;
    const relStart = Math.max(0, startTime - matchStart);
    const relEnd = Math.max(0, endTime - matchStart);
    const tickCount = 5;

    for (let i = 0; i <= tickCount; i++) {
        const time = relStart + ((relEnd - relStart) / tickCount) * i;
        const span = document.createElement('span');
        span.textContent = formatTime(time);
        container.appendChild(span);
    }
}

function updateHealthArmorGraph(startTime, endTime) {
    const container = document.getElementById('health-armor-graph');
    container.innerHTML = '';

    const buckets = timelineState.buckets;
    const teams = timelineState.teams;

    if (!buckets || buckets.length === 0 || teams.length < 2) return;

    // Filter buckets within range
    const filteredBuckets = buckets.filter(b =>
        b.startTime >= startTime && b.endTime <= endTime
    );

    if (filteredBuckets.length === 0) return;

    // Apply dynamic binning based on selection duration
    const selectionDuration = endTime - startTime;
    const binSize = getOptimalBinSize(selectionDuration);
    const displayBuckets = aggregateDetailBuckets(filteredBuckets, binSize, teams);

    // Find max value for scaling (team total health + armor)
    // For 4 players: max health ~400 (4*100), max armor ~800 (4*200)
    let maxValue = 400; // Use 400 as reasonable default for team total

    for (const bucket of displayBuckets) {
        const td = bucket.teamData || {};
        const teamA = td[teams[0]] || {};
        const teamB = td[teams[1]] || {};
        const teamATotal = (teamA.totalHealth || 0) + (teamA.totalArmor || 0);
        const teamBTotal = (teamB.totalHealth || 0) + (teamB.totalArmor || 0);
        maxValue = Math.max(maxValue, teamATotal, teamBTotal);
    }

    // Update Y-axis labels
    document.getElementById('health-y-top').textContent = maxValue;
    document.getElementById('health-y-bottom').textContent = maxValue;

    const barHeight = 90; // pixels for max value

    // Create diverging bars (Team A up, Team B down)
    for (const bucket of displayBuckets) {
        const bar = document.createElement('div');
        bar.className = 'diverging-bar';

        const td = bucket.teamData || {};
        const teamA = td[teams[0]] || {};
        const teamB = td[teams[1]] || {};

        // Helper to add armor segments by type
        const addArmorSegments = (teamData, container) => {
            const armorByType = teamData.armorByType || {};
            const totalArmor = teamData.totalArmor || 0;
            const raCount = armorByType.ra || 0;
            const yaCount = armorByType.ya || 0;
            const gaCount = armorByType.ga || 0;
            const totalPlayers = raCount + yaCount + gaCount;

            if (totalPlayers > 0 && totalArmor > 0) {
                // Distribute armor proportionally by type
                const raArmor = (raCount / totalPlayers) * totalArmor;
                const yaArmor = (yaCount / totalPlayers) * totalArmor;
                const gaArmor = (gaCount / totalPlayers) * totalArmor;

                // Add RA first (closest to axis), then YA, then GA
                if (gaArmor > 0) {
                    const seg = document.createElement('div');
                    seg.className = 'bar-segment ga';
                    seg.style.height = `${(gaArmor / maxValue) * barHeight}px`;
                    container.appendChild(seg);
                }
                if (yaArmor > 0) {
                    const seg = document.createElement('div');
                    seg.className = 'bar-segment ya';
                    seg.style.height = `${(yaArmor / maxValue) * barHeight}px`;
                    container.appendChild(seg);
                }
                if (raArmor > 0) {
                    const seg = document.createElement('div');
                    seg.className = 'bar-segment ra';
                    seg.style.height = `${(raArmor / maxValue) * barHeight}px`;
                    container.appendChild(seg);
                }
            } else if (totalArmor > 0) {
                // Fallback to generic armor if no type breakdown
                const seg = document.createElement('div');
                seg.className = 'bar-segment armor';
                seg.style.height = `${(totalArmor / maxValue) * barHeight}px`;
                container.appendChild(seg);
            }
        };

        // Team A goes up (above center axis) - health closer to axis, armor on top
        const topContainer = document.createElement('div');
        topContainer.className = 'diverging-bar-top';

        if ((teamA.totalHealth || 0) > 0) {
            const seg = document.createElement('div');
            seg.className = 'bar-segment health';
            seg.style.height = `${((teamA.totalHealth || 0) / maxValue) * barHeight}px`;
            topContainer.appendChild(seg);
        }
        addArmorSegments(teamA, topContainer);

        // Team B goes down (below center axis)
        const bottomContainer = document.createElement('div');
        bottomContainer.className = 'diverging-bar-bottom';

        if ((teamB.totalHealth || 0) > 0) {
            const seg = document.createElement('div');
            seg.className = 'bar-segment health';
            seg.style.height = `${((teamB.totalHealth || 0) / maxValue) * barHeight}px`;
            bottomContainer.appendChild(seg);
        }
        addArmorSegments(teamB, bottomContainer);

        bar.appendChild(topContainer);
        bar.appendChild(bottomContainer);
        container.appendChild(bar);
    }

    // Update health axis
    updateHealthAxis(startTime, endTime);
}

function updateHealthAxis(startTime, endTime) {
    const container = document.getElementById('health-axis');
    container.innerHTML = '';

    const matchStart = timelineState.matchStartTime;
    const relStart = Math.max(0, startTime - matchStart);
    const relEnd = Math.max(0, endTime - matchStart);
    const tickCount = 5;

    for (let i = 0; i <= tickCount; i++) {
        const time = relStart + ((relEnd - relStart) / tickCount) * i;
        const span = document.createElement('span');
        span.textContent = formatTime(time);
        container.appendChild(span);
    }
}

function updateFragsGraph(startTime, endTime) {
    const container = document.getElementById('frags-graph');
    if (!container) return;
    container.innerHTML = '';

    const teams = timelineState.teams;
    if (teams.length < 2) return;

    // Use frag events from timeline analysis (from stat tracking)
    const fragEvents = timelineState.fragEvents || [];

    // Filter frags to selection window
    const filteredFrags = fragEvents.filter(f => f.time >= startTime && f.time <= endTime);

    // Calculate dynamic bucket duration based on selection (use 15s bins for frags)
    // For frag counts, we want larger bins than the activity graph
    const selectionDuration = endTime - startTime;
    const baseBinSize = getOptimalBinSize(selectionDuration);
    const bucketDuration = Math.max(15, baseBinSize * 5); // 15s minimum, scale with selection

    const startBucket = Math.floor(startTime / bucketDuration);
    const endBucket = Math.ceil(endTime / bucketDuration);
    const numBuckets = endBucket - startBucket;

    if (numBuckets <= 0) return;

    // Count frags per bucket per team
    const teamAFrags = new Array(numBuckets).fill(0);
    const teamBFrags = new Array(numBuckets).fill(0);

    for (const frag of filteredFrags) {
        const bucketIdx = Math.floor(frag.time / bucketDuration) - startBucket;
        if (bucketIdx >= 0 && bucketIdx < numBuckets) {
            if (frag.team === teams[0]) {
                teamAFrags[bucketIdx]++;
            } else if (frag.team === teams[1]) {
                teamBFrags[bucketIdx]++;
            }
        }
    }

    // Find max frags for scaling
    let maxFrags = 5;
    for (let i = 0; i < numBuckets; i++) {
        maxFrags = Math.max(maxFrags, teamAFrags[i], teamBFrags[i]);
    }

    // Update Y-axis labels
    const yTop = document.getElementById('frags-y-top');
    const yBottom = document.getElementById('frags-y-bottom');
    if (yTop) yTop.textContent = maxFrags;
    if (yBottom) yBottom.textContent = maxFrags;

    // Update legend team names
    const legendA = document.getElementById('legend-frags-team-a');
    const legendB = document.getElementById('legend-frags-team-b');
    if (legendA) legendA.textContent = `${teams[0]} ↑`;
    if (legendB) legendB.textContent = `${teams[1]} ↓`;

    const barHeight = 90;

    // Create diverging bars
    for (let i = 0; i < numBuckets; i++) {
        const bar = document.createElement('div');
        bar.className = 'diverging-bar';

        // Team A up
        const topContainer = document.createElement('div');
        topContainer.className = 'diverging-bar-top';
        if (teamAFrags[i] > 0) {
            const seg = document.createElement('div');
            seg.className = 'bar-segment frags';
            seg.style.height = `${(teamAFrags[i] / maxFrags) * barHeight}px`;
            topContainer.appendChild(seg);
        }

        // Team B down
        const bottomContainer = document.createElement('div');
        bottomContainer.className = 'diverging-bar-bottom';
        if (teamBFrags[i] > 0) {
            const seg = document.createElement('div');
            seg.className = 'bar-segment frags';
            seg.style.height = `${(teamBFrags[i] / maxFrags) * barHeight}px`;
            bottomContainer.appendChild(seg);
        }

        bar.appendChild(topContainer);
        bar.appendChild(bottomContainer);
        container.appendChild(bar);
    }

    // Update axis for selection window
    updateFragsAxis(startTime, endTime);
}

function updateFragsAxis(startTime, endTime) {
    const container = document.getElementById('frags-axis');
    if (!container) return;
    container.innerHTML = '';

    const matchStart = timelineState.matchStartTime;
    const relStart = Math.max(0, startTime - matchStart);
    const relEnd = Math.max(0, endTime - matchStart);
    const tickCount = 5;

    for (let i = 0; i <= tickCount; i++) {
        const time = relStart + ((relEnd - relStart) / tickCount) * i;
        const span = document.createElement('span');
        span.textContent = formatTime(time);
        container.appendChild(span);
    }
}

function updateScoreTimeline(startTime, endTime) {
    const container = document.getElementById('score-graph');
    if (!container) return;
    container.innerHTML = '';

    const teams = timelineState.teams;
    if (teams.length < 2) return;

    // Use frag events from timeline analysis (from stat tracking), sorted by time
    const fragEvents = (timelineState.fragEvents || []).slice().sort((a, b) => a.time - b.time);

    // Calculate score at the start of selection (based on all frags before startTime)
    let scoreAtStart = 0;
    for (const frag of fragEvents) {
        if (frag.time >= startTime) break;
        if (frag.team === teams[0]) {
            scoreAtStart++;
        } else if (frag.team === teams[1]) {
            scoreAtStart--;
        }
    }

    // Calculate cumulative score within selection window
    // Positive = Team A leading, Negative = Team B leading
    let scoreDiff = scoreAtStart;
    const scorePoints = [];

    // Add initial point at selection start
    scorePoints.push({ time: startTime, diff: scoreDiff });

    for (const frag of fragEvents) {
        if (frag.time < startTime) continue;
        if (frag.time > endTime) break;
        if (frag.team === teams[0]) {
            scoreDiff++;
        } else if (frag.team === teams[1]) {
            scoreDiff--;
        }
        scorePoints.push({ time: frag.time, diff: scoreDiff });
    }

    // Add final point
    scorePoints.push({ time: endTime, diff: scoreDiff });

    if (scorePoints.length === 0) return;

    // Find max absolute difference within selection for scaling
    let maxDiff = 10;
    for (const pt of scorePoints) {
        maxDiff = Math.max(maxDiff, Math.abs(pt.diff));
    }

    // Update Y-axis labels
    const yTop = document.getElementById('score-y-top');
    const yBottom = document.getElementById('score-y-bottom');
    if (yTop) yTop.textContent = `+${maxDiff}`;
    if (yBottom) yBottom.textContent = `-${maxDiff}`;

    // Update legend team names
    const legendA = document.getElementById('legend-score-team-a');
    const legendB = document.getElementById('legend-score-team-b');
    if (legendA) legendA.textContent = `${teams[0]} leading ↑`;
    if (legendB) legendB.textContent = `${teams[1]} leading ↓`;

    // Calculate dynamic bucket duration based on selection
    const selectionDuration = endTime - startTime;
    const baseBinSize = getOptimalBinSize(selectionDuration);
    const bucketDuration = Math.max(5, baseBinSize * 2); // 5s minimum for score, scale with selection

    const numBuckets = Math.ceil((endTime - startTime) / bucketDuration);
    const barHeight = 90; // pixels for max value

    for (let i = 0; i < numBuckets; i++) {
        const bucketStart = startTime + i * bucketDuration;

        // Find score at bucket midpoint
        const bucketMid = bucketStart + bucketDuration / 2;
        let bucketScore = scoreAtStart;
        for (const pt of scorePoints) {
            if (pt.time <= bucketMid) {
                bucketScore = pt.diff;
            } else {
                break;
            }
        }

        const bar = document.createElement('div');
        bar.className = 'score-bar';

        const fill = document.createElement('div');
        fill.className = 'score-bar-fill';

        const heightPct = Math.abs(bucketScore) / maxDiff;
        const heightPx = heightPct * barHeight;

        if (bucketScore > 0) {
            fill.classList.add('positive');
            fill.style.height = `${heightPx}px`;
        } else if (bucketScore < 0) {
            fill.classList.add('negative');
            fill.style.height = `${heightPx}px`;
        }

        bar.appendChild(fill);
        container.appendChild(bar);
    }

    // Update axis for selection window
    updateScoreAxis(startTime, endTime);
}

function updateScoreAxis(startTime, endTime) {
    const container = document.getElementById('score-axis');
    if (!container) return;
    container.innerHTML = '';

    const matchStart = timelineState.matchStartTime;
    const relStart = Math.max(0, startTime - matchStart);
    const relEnd = Math.max(0, endTime - matchStart);
    const tickCount = 5;

    for (let i = 0; i <= tickCount; i++) {
        const time = relStart + ((relEnd - relStart) / tickCount) * i;
        const span = document.createElement('span');
        span.textContent = formatTime(time);
        container.appendChild(span);
    }
}

// =============================================================================
// Map Visualization
// =============================================================================

// Map View State
let mapState = {
    canvas: null,
    ctx: null,
    locations: [],
    bounds: { minX: 0, maxX: 0, minY: 0, maxY: 0 },
    currentTime: 0,
    isPlaying: false,
    playInterval: null,
    showTracks: false,
    tracks: {}, // playerName -> [{x, y}]
    teams: [],
    playerSymbols: {}, // playerName -> { symbol, team, teamIdx }
    initialized: false
};

const PLAYER_SYMBOLS = ['*', 'x', '+', 'o', '◆', '▲', '●', '■'];

function initMapView(result) {
    if (!result.timelineAnalysis) return;

    mapState.canvas = document.getElementById('map-canvas');
    if (!mapState.canvas) return;
    mapState.ctx = mapState.canvas.getContext('2d');

    // Get location data from timeline analysis
    const timeline = result.timelineAnalysis;
    mapState.locations = timeline.locationData || [];

    // Show/hide no-data message
    const noDataMsg = document.getElementById('map-no-data');
    if (noDataMsg) {
        noDataMsg.style.display = mapState.locations.length === 0 ? 'block' : 'none';
    }

    // Calculate bounds from locations and player positions
    calculateMapBounds(result);

    // Get teams from demoInfo or match
    if (result.demoInfo?.teams) {
        mapState.teams = result.demoInfo.teams;
    } else if (result.match?.teams) {
        mapState.teams = result.match.teams.map(t => t.name);
    } else {
        mapState.teams = [];
    }

    // Assign symbols to players
    assignPlayerSymbols(result);

    // Set up time controls (only once)
    if (!mapState.initialized) {
        setupMapTimeControls(result);
        mapState.initialized = true;
    } else {
        // Update slider for new demo
        updateMapSliderRange(result);
    }

    // Build powerup event list
    buildMapPowerupList(result);

    // Reset tracks
    mapState.tracks = {};
    const showTracksCheckbox = document.getElementById('map-show-tracks');
    if (showTracksCheckbox) {
        showTracksCheckbox.checked = false;
        mapState.showTracks = false;
    }

    // Initial render at match start
    mapState.currentTime = timeline.matchStartTime || 0;
    const slider = document.getElementById('map-timeline-slider');
    if (slider) slider.value = mapState.currentTime;

    renderMap(mapState.currentTime);
}

function calculateMapBounds(result) {
    let minX = Infinity, maxX = -Infinity;
    let minY = Infinity, maxY = -Infinity;

    // From locations
    for (const loc of mapState.locations) {
        minX = Math.min(minX, loc.x);
        maxX = Math.max(maxX, loc.x);
        minY = Math.min(minY, loc.y);
        maxY = Math.max(maxY, loc.y);
    }

    // From player positions in timeline
    const buckets = result.timelineAnalysis?.buckets || [];
    for (const bucket of buckets) {
        for (const [name, data] of Object.entries(bucket.playerData || {})) {
            if (data.x !== 0 || data.y !== 0) {
                minX = Math.min(minX, data.x);
                maxX = Math.max(maxX, data.x);
                minY = Math.min(minY, data.y);
                maxY = Math.max(maxY, data.y);
            }
        }
    }

    // Handle case where no data found
    if (minX === Infinity) {
        minX = -1000; maxX = 1000;
        minY = -1000; maxY = 1000;
    }

    // Add padding (10%)
    const padX = (maxX - minX) * 0.1;
    const padY = (maxY - minY) * 0.1;

    mapState.bounds = {
        minX: minX - padX,
        maxX: maxX + padX,
        minY: minY - padY,
        maxY: maxY + padY
    };
}

function worldToCanvas(x, y) {
    const { minX, maxX, minY, maxY } = mapState.bounds;
    const canvas = mapState.canvas;

    const worldWidth = maxX - minX;
    const worldHeight = maxY - minY;

    const scaleX = canvas.width / worldWidth;
    const scaleY = canvas.height / worldHeight;
    const scale = Math.min(scaleX, scaleY);

    const offsetX = (canvas.width - worldWidth * scale) / 2;
    const offsetY = (canvas.height - worldHeight * scale) / 2;

    return {
        x: offsetX + (x - minX) * scale,
        y: canvas.height - (offsetY + (y - minY) * scale) // Flip Y
    };
}

function assignPlayerSymbols(result) {
    const demoInfo = result.demoInfo;
    const players = demoInfo?.players || [];

    mapState.playerSymbols = {};

    // Group players by team
    const teamPlayers = {};
    for (const team of mapState.teams) {
        teamPlayers[team] = [];
    }

    for (const player of players) {
        if (player.team && teamPlayers[player.team]) {
            teamPlayers[player.team].push(player.name);
        }
    }

    // Assign symbols
    for (let teamIdx = 0; teamIdx < mapState.teams.length; teamIdx++) {
        const team = mapState.teams[teamIdx];
        const playerList = teamPlayers[team] || [];
        playerList.forEach((name, i) => {
            mapState.playerSymbols[name] = {
                symbol: PLAYER_SYMBOLS[i % PLAYER_SYMBOLS.length],
                team: team,
                teamIdx: teamIdx
            };
        });
    }

    // Build legend
    buildMapLegend();
}

function buildMapLegend() {
    const legend = document.getElementById('map-legend');
    if (!legend) return;

    legend.innerHTML = '<h4>Players</h4>';

    for (let teamIdx = 0; teamIdx < mapState.teams.length; teamIdx++) {
        const team = mapState.teams[teamIdx];
        const teamDiv = document.createElement('div');
        teamDiv.className = 'map-legend-team';

        const teamColor = teamIdx === 0 ? 'player-red' : 'player-blue';
        teamDiv.innerHTML = `<div class="map-legend-team-name ${teamColor}">${escapeHtml(team)}</div>`;

        for (const [name, info] of Object.entries(mapState.playerSymbols)) {
            if (info.team === team) {
                const item = document.createElement('div');
                item.className = 'map-legend-item';
                item.innerHTML = `
                    <span class="map-legend-symbol ${teamColor}">${info.symbol}</span>
                    <span>${escapeHtml(name)}</span>
                `;
                teamDiv.appendChild(item);
            }
        }

        legend.appendChild(teamDiv);
    }
}

function renderMap(time) {
    const ctx = mapState.ctx;
    const canvas = mapState.canvas;

    if (!ctx || !canvas) return;

    // Clear
    ctx.fillStyle = '#0a0a15';
    ctx.fillRect(0, 0, canvas.width, canvas.height);

    // Draw location labels
    ctx.font = '11px monospace';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';

    for (const loc of mapState.locations) {
        const pos = worldToCanvas(loc.x, loc.y);
        // Draw subtle dot for location
        ctx.fillStyle = '#333';
        ctx.beginPath();
        ctx.arc(pos.x, pos.y, 2, 0, Math.PI * 2);
        ctx.fill();
        // Draw label
        ctx.fillStyle = '#555';
        ctx.fillText(loc.name, pos.x, pos.y - 10);
    }

    // Get player positions at this time
    const bucket = findBucketAtTime(time);

    // Draw tracks if enabled
    if (mapState.showTracks) {
        drawTracks(ctx);
    }

    // Draw players
    if (bucket) {
        ctx.font = 'bold 18px monospace';
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';

        for (const [name, data] of Object.entries(bucket.playerData || {})) {
            if (data.x === 0 && data.y === 0) continue;

            const pos = worldToCanvas(data.x, data.y);
            const symbolInfo = mapState.playerSymbols[name];

            if (symbolInfo) {
                // Team color
                ctx.fillStyle = symbolInfo.teamIdx === 0 ? '#ff5050' : '#50a0ff';
                ctx.fillText(symbolInfo.symbol, pos.x, pos.y);

                // Add to track if showing tracks
                if (mapState.showTracks) {
                    if (!mapState.tracks[name]) mapState.tracks[name] = [];
                    const lastPos = mapState.tracks[name][mapState.tracks[name].length - 1];
                    // Only add if moved significantly
                    if (!lastPos || Math.abs(lastPos.x - pos.x) > 2 || Math.abs(lastPos.y - pos.y) > 2) {
                        mapState.tracks[name].push({
                            x: pos.x,
                            y: pos.y,
                            teamIdx: symbolInfo.teamIdx
                        });
                    }
                }
            }
        }
    }

    // Update time display
    const matchStart = currentResult?.timelineAnalysis?.matchStartTime || 0;
    const relTime = time - matchStart;
    const timeDisplay = document.getElementById('map-current-time');
    if (timeDisplay) {
        timeDisplay.textContent = formatTime(Math.max(0, relTime));
    }
}

function drawTracks(ctx) {
    for (const [name, points] of Object.entries(mapState.tracks)) {
        if (points.length < 2) continue;

        ctx.beginPath();
        ctx.strokeStyle = points[0].teamIdx === 0
            ? 'rgba(255, 80, 80, 0.4)'
            : 'rgba(80, 160, 255, 0.4)';
        ctx.lineWidth = 2;

        ctx.moveTo(points[0].x, points[0].y);
        for (let i = 1; i < points.length; i++) {
            ctx.lineTo(points[i].x, points[i].y);
        }
        ctx.stroke();
    }
}

function findBucketAtTime(time) {
    const buckets = currentResult?.timelineAnalysis?.buckets || [];
    for (const bucket of buckets) {
        if (time >= bucket.startTime && time < bucket.endTime) {
            return bucket;
        }
    }
    // Return last bucket if past end
    return buckets.length > 0 ? buckets[buckets.length - 1] : null;
}

function setupMapTimeControls(result) {
    updateMapSliderRange(result);

    const slider = document.getElementById('map-timeline-slider');
    if (slider) {
        slider.addEventListener('input', (e) => {
            mapState.currentTime = parseFloat(e.target.value);
            renderMap(mapState.currentTime);
        });
    }

    const playPauseBtn = document.getElementById('map-play-pause');
    if (playPauseBtn) {
        playPauseBtn.addEventListener('click', toggleMapPlayback);
    }

    const jumpBackBtn = document.getElementById('map-jump-back');
    if (jumpBackBtn) {
        jumpBackBtn.addEventListener('click', () => jumpMapTime(-10));
    }

    const jumpForwardBtn = document.getElementById('map-jump-forward');
    if (jumpForwardBtn) {
        jumpForwardBtn.addEventListener('click', () => jumpMapTime(10));
    }

    const showTracksCheckbox = document.getElementById('map-show-tracks');
    if (showTracksCheckbox) {
        showTracksCheckbox.addEventListener('change', (e) => {
            mapState.showTracks = e.target.checked;
            if (!mapState.showTracks) {
                mapState.tracks = {};
            }
            renderMap(mapState.currentTime);
        });
    }

    const resetTracksBtn = document.getElementById('map-reset-tracks');
    if (resetTracksBtn) {
        resetTracksBtn.addEventListener('click', () => {
            mapState.tracks = {};
            renderMap(mapState.currentTime);
        });
    }
}

function updateMapSliderRange(result) {
    const slider = document.getElementById('map-timeline-slider');
    if (!slider) return;

    const duration = result.duration || 600;
    const matchStart = result.timelineAnalysis?.matchStartTime || 0;

    slider.min = matchStart;
    slider.max = duration;
    slider.value = matchStart;
    mapState.currentTime = matchStart;
}

function toggleMapPlayback() {
    const btn = document.getElementById('map-play-pause');
    if (!btn) return;

    if (mapState.isPlaying) {
        clearInterval(mapState.playInterval);
        mapState.isPlaying = false;
        btn.textContent = '▶';
    } else {
        mapState.isPlaying = true;
        btn.textContent = '⏸';
        mapState.playInterval = setInterval(() => {
            mapState.currentTime += 1;
            const slider = document.getElementById('map-timeline-slider');
            if (slider) {
                if (mapState.currentTime > parseFloat(slider.max)) {
                    mapState.currentTime = parseFloat(slider.min);
                    mapState.tracks = {}; // Reset tracks on loop
                }
                slider.value = mapState.currentTime;
            }
            renderMap(mapState.currentTime);
        }, 1000); // 1 second per second (real-time)
    }
}

function jumpMapTime(delta) {
    const slider = document.getElementById('map-timeline-slider');
    if (!slider) return;

    mapState.currentTime = Math.max(
        parseFloat(slider.min),
        Math.min(parseFloat(slider.max), mapState.currentTime + delta)
    );
    slider.value = mapState.currentTime;
    renderMap(mapState.currentTime);
}

function buildMapPowerupList(result) {
    const list = document.getElementById('map-powerup-events');
    if (!list) return;

    list.innerHTML = '';

    const events = result.timelineAnalysis?.powerupEvents || [];
    const matchStart = result.timelineAnalysis?.matchStartTime || 0;

    if (events.length === 0) {
        list.innerHTML = '<li style="color: #666; font-style: italic;">No powerup events</li>';
        return;
    }

    for (const event of events) {
        const li = document.createElement('li');
        const relTime = Math.max(0, event.time - matchStart);
        li.innerHTML = `
            <span class="time-cell">${formatTime(relTime)}</span>
            <span class="powerup-cell ${event.powerupType}">${getPowerupDisplay(event.powerupType)}</span>
            <span>${escapeHtml(event.playerName || 'Unknown')}</span>
        `;
        li.addEventListener('click', () => {
            mapState.currentTime = event.time;
            const slider = document.getElementById('map-timeline-slider');
            if (slider) slider.value = event.time;
            renderMap(event.time);
        });
        list.appendChild(li);
    }
}
