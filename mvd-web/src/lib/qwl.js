// qwl.js — QWLeague (qwleague.com) on the hub's games: a game played on one
// of QWLeague's servers is known by its hub row's server hostname. DOM-free.

// The substring of a hub row's server_hostname that the "QWL only" search
// filters on.
export const QWL_SERVER = 'qwleague.com';

// isQwlGame: a hub row of a game played on a QWLeague server.
/** @param {import('../data/hub.js').HubGame} game */
export function isQwlGame(game) {
    const host = String(game?.server_hostname || '').toLowerCase().split(':')[0];
    return host === QWL_SERVER || host.endsWith('.' + QWL_SERVER);
}
