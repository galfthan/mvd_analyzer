// loc-graph-view.js — the loc graph's Cytoscape instance (Cytoscape.js +
// fcose, vendored classic scripts loaded by index.html: the `cytoscape` and
// `cytoscapeFcose` globals). LocGraphTab creates one per demo and destroys it
// when the demo changes or the tab unmounts; the element set, stylesheet and
// layouts come from src/lib/locgraph.js.

import { locGraphLayout, zoomedLabelStyle, nodeTooltipHtml, edgeTooltipHtml } from '../../lib/locgraph.js';

// cytoscape-fcose registers itself on the global `cytoscape` object when
// loaded; guard the registration so re-loading the file during dev doesn't
// throw.
function registerCytoscapeExtensions() {
    if (typeof cytoscape === 'undefined') return;
    if (typeof cytoscapeFcose !== 'undefined' && !cytoscape.__fcoseRegistered) {
        cytoscape.use(cytoscapeFcose);
        cytoscape.__fcoseRegistered = true;
    }
}

// The hover tooltip: created on the first graph and kept (next to the
// canvas, inside its container) for every later one.
let tooltip = null;

export class LocGraphView {
    // `labelSize()` reads the user's label size (the zoom counter-scaling
    // starts from it).
    constructor(container, labelSize) {
        this.container = container;
        this.labelSize = labelSize;
        this.cy = null;
        this.labelRaf = 0;
    }

    // refresh builds the instance on first call, otherwise swaps in the new
    // element set + style, then runs the layout. Keeping the instance alive
    // preserves pan/zoom state when the user toggles a filter.
    refresh(elements, style, layout) {
        registerCytoscapeExtensions();
        if (typeof cytoscape === 'undefined') return;
        if (!this.cy) {
            this.cy = cytoscape({
                container: this.container,
                elements,
                style,
                layout: { name: 'preset' },
                wheelSensitivity: 0.2,
                minZoom: 0.1,
                maxZoom: 4,
            });
            this.attachInteractions();
            this.cy.on('zoom', () => this.scheduleLabelSize());
        } else {
            this.cy.batch(() => {
                this.cy.elements().remove();
                this.cy.add(elements);
                this.cy.style(style);
            });
        }
        this.runLayout(layout, false);
    }

    runLayout(name, animate) {
        const cy = this.cy;
        if (!cy) return;
        const l = cy.layout(locGraphLayout(name, animate));
        l.one('layoutstop', () => this.updateLabelSize());
        l.run();
        // Non-animated layouts don't always emit layoutstop synchronously;
        // run once now so the first paint already has correct sizing.
        this.updateLabelSize();
    }

    setStyle(style) {
        if (!this.cy) return;
        this.cy.style(style);
        this.updateLabelSize();
    }

    fit() {
        if (this.cy) this.cy.fit(undefined, 30);
    }

    // reveal: the container may have been resized (or drawn at zero size)
    // while hidden.
    reveal() {
        if (!this.cy) return;
        this.cy.resize();
        this.cy.fit(undefined, 30);
    }

    destroy() {
        if (this.cy) {
            try { this.cy.destroy(); } catch (_) {}
            this.cy = null;
        }
        this.container.innerHTML = '';
    }

    scheduleLabelSize() {
        if (this.labelRaf) return;
        this.labelRaf = requestAnimationFrame(() => {
            this.labelRaf = 0;
            this.updateLabelSize();
        });
    }

    // Labels keep their on-screen size through zoom (zoomedLabelStyle).
    updateLabelSize() {
        const cy = this.cy;
        if (!cy) return;
        const st = zoomedLabelStyle(this.labelSize(), cy.zoom());
        cy.batch(() => {
            cy.nodes().style(st);
        });
    }

    // Click: show a tooltip with top-5 connections. Hover: fade the rest of the
    // graph so the node's neighborhood is clear.
    attachInteractions() {
        // NB: not built on app.js's attachCanvasTooltip — this rides
        // Cytoscape's own node/edge mouseover delegation and renderedPosition
        // (plus a graph-fade side effect), not a raw canvas mousemove
        // hit-test, so the shared helper wouldn't fit without contortion.
        const cy = this.cy;
        const container = this.container;
        if (!tooltip) {
            tooltip = document.createElement('div');
            tooltip.className = 'locgraph-tooltip';
            tooltip.style.display = 'none';
            container.parentElement.appendChild(tooltip);
        }
        const tip = tooltip;

        const hideTip = () => { tip.style.display = 'none'; };
        const showTipAt = (evt, html) => {
            tip.textContent = '';
            tip.innerHTML = html;
            tip.style.display = 'block';
            // Position via renderedPosition so it tracks pan/zoom.
            const rect = container.getBoundingClientRect();
            const x = evt.renderedPosition ? evt.renderedPosition.x : 0;
            const y = evt.renderedPosition ? evt.renderedPosition.y : 0;
            tip.style.left = (x + 12) + 'px';
            tip.style.top = (rect.height - y - 12) + 'px'; // bottom-up coords
            // Simpler: use originalEvent client position if available.
            if (evt.originalEvent) {
                tip.style.left = (evt.originalEvent.clientX - rect.left + 12) + 'px';
                tip.style.top = (evt.originalEvent.clientY - rect.top + 12) + 'px';
            }
        };

        cy.on('mouseover', 'node', (evt) => {
            const node = evt.target;
            cy.elements().addClass('faded');
            node.removeClass('faded').addClass('highlight');
            node.connectedEdges().removeClass('faded').addClass('highlight');
            node.connectedEdges().connectedNodes().removeClass('faded').addClass('highlight');
            showTipAt(evt, nodeTooltipHtml(node.data()));
        });
        cy.on('mouseover', 'edge', (evt) => {
            const edge = evt.target;
            cy.elements().addClass('faded');
            edge.removeClass('faded').addClass('highlight');
            edge.source().removeClass('faded').addClass('highlight');
            edge.target().removeClass('faded').addClass('highlight');
            showTipAt(evt, edgeTooltipHtml(edge.data()));
        });
        cy.on('mouseout', 'node, edge', () => {
            cy.elements().removeClass('faded').removeClass('highlight');
            hideTip();
        });
        cy.on('tap', (evt) => {
            if (evt.target === cy) hideTip();
        });
    }
}
