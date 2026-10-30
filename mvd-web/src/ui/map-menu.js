// map-menu.js — the item type of the map's and the player's menus
// (MapMenu.svelte); hosts build lists of them. `keys` names the keyboard
// shortcut an item has (shown at its right); an `info` item is a shortcut
// with nothing to click.

/**
 * @typedef {{ kind: 'check' | 'radio', label: string, title?: string, on: boolean, run: () => void, keys?: string }
 *   | { kind: 'action', label: string, title?: string, run: () => void, close?: boolean, keys?: string }
 *   | { kind: 'info', label: string, title?: string, keys: string }
 *   | { kind: 'sep' }} MenuItem
 */

export {};
