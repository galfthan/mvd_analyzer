// MVD Analyzer Dashboard

document.addEventListener('DOMContentLoaded', () => {
    setupFileUpload();
});

function setupFileUpload() {
    const dropZone = document.getElementById('drop-zone');
    const fileInput = document.getElementById('file-input');

    // Handle file selection
    fileInput.addEventListener('change', (e) => {
        if (e.target.files.length > 0) {
            uploadFile(e.target.files[0]);
        }
    });

    // Handle drag and drop
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

        displayResults(data.result);
    } catch (error) {
        status.textContent = 'Error: ' + error.message;
        status.className = 'status error';
    }
}

function displayResults(result) {
    document.getElementById('results-section').style.display = 'block';

    // Match summary
    if (result.match) {
        document.getElementById('map-name').textContent = result.match.map || '-';
        document.getElementById('duration').textContent = formatDuration(result.duration);
    }

    // Total frags
    if (result.frags) {
        document.getElementById('total-frags').textContent = result.frags.totalFrags;
    }

    // Teams
    if (result.match && result.match.teams) {
        displayTeams(result.match.teams);
    }

    // Scoreboard
    if (result.frags && result.frags.byPlayer) {
        displayScoreboard(result.frags.byPlayer, result.match ? result.match.players : []);
    }

    // Weapons chart
    if (result.frags && result.frags.byWeapon) {
        displayWeaponsChart(result.frags.byWeapon);
    }

    // Recent frags
    if (result.frags && result.frags.frags) {
        displayFrags(result.frags.frags);
    }
}

function displayTeams(teams) {
    const container = document.getElementById('teams-list');
    container.innerHTML = '';

    // Sort by frags
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

function displayScoreboard(byPlayer, players) {
    const tbody = document.getElementById('scoreboard-body');
    tbody.innerHTML = '';

    // Build player data with team info
    const playerData = [];
    for (const [name, stats] of Object.entries(byPlayer)) {
        // Skip weird entries
        if (name.includes("'s quad") || name === 'teammate' || name === 'his teammate') {
            continue;
        }

        const playerInfo = players.find(p => p.name === name);
        playerData.push({
            name: name,
            team: playerInfo ? playerInfo.team : '',
            kills: stats.kills,
            deaths: stats.deaths,
            frags: playerInfo ? playerInfo.frags : (stats.kills - stats.deaths)
        });
    }

    // Sort by kills
    playerData.sort((a, b) => b.kills - a.kills);

    playerData.forEach(player => {
        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td>${escapeHtml(player.name)}</td>
            <td>${escapeHtml(player.team)}</td>
            <td>${player.kills}</td>
            <td>${player.deaths}</td>
            <td>${player.frags}</td>
        `;
        tbody.appendChild(tr);
    });
}

function displayWeaponsChart(byWeapon) {
    const container = document.getElementById('weapons-chart');
    container.innerHTML = '';

    // Sort by count
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

function displayFrags(frags) {
    const container = document.getElementById('frags-list');
    container.innerHTML = '';

    // Show last 50 frags
    const recent = frags.slice(-50).reverse();

    recent.forEach(frag => {
        const div = document.createElement('div');
        div.className = 'frag-item' + (frag.isTeamKill ? ' teamkill' : '') + (frag.isSuicide ? ' suicide' : '');

        const time = formatTime(frag.time);
        let text;
        if (frag.isSuicide) {
            text = `${escapeHtml(frag.victim)} suicided (${frag.weapon})`;
        } else {
            text = `${escapeHtml(frag.killer)} killed ${escapeHtml(frag.victim)} (${frag.weapon})`;
        }

        div.innerHTML = `
            <span class="frag-time">[${time}]</span>
            <span class="frag-text">${text}</span>
        `;
        container.appendChild(div);
    });
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
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
