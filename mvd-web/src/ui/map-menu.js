// map-menu.js — the item type of the map's right-click menu
// (MapMenu.svelte); hosts build lists of them.

/**
 * @typedef {{ kind: 'check' | 'radio', label: string, title?: string, on: boolean, run: () => void }
 *   | { kind: 'action', label: string, title?: string, run: () => void, close?: boolean }
 *   | { kind: 'sep' }} MenuItem
 */

export {};
