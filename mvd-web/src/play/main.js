// The light player's entry (play.html): the playlist in the URL's
// #playlist= (src/lib/playlist.js encodePlaylist), mounted as PlayApp.
// Nothing of the analyzer loads here.
import { mount } from 'svelte';
import PlayApp from './PlayApp.svelte';
import { decodePlaylist } from '../lib/playlist.js';

const code = (location.hash.match(/^#playlist=([A-Za-z0-9_-]+)$/) || [])[1] || '';
const clips = (code && await decodePlaylist(code)) || [];
mount(PlayApp, { target: /** @type {HTMLElement} */ (document.getElementById('play')), props: { clips, code } });
