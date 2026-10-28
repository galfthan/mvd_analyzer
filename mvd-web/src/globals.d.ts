// Ambient declarations for the checker (jsconfig.json checkJs). Types only:
// nothing here exists at runtime.

// Cytoscape and its fcose layout: vendored classic scripts loaded by
// index.html (static/vendor/), not modules, and shipped without types —
// only src/tabs/locgraph/loc-graph-view.js touches them.
declare const cytoscape: any;
declare const cytoscapeFcose: any;

// What src/main.js and src/app/timing.js publish on window for the
// browser console and the headless checks. No page code reads them.
interface Window {
    MvdState: typeof import('./state/index.js');
    MvdPlayer: typeof import('./player/index.js').engine;
    MvdMapTab: typeof import('./tabs/map/map-model.js');
    mvdData: typeof import('./app/source.js').mvdData;
    loadDemo: typeof import('./app/load.js').loadDemo;
    __mvdTimings: import('./app/timing.js').LoadTimingSummary;
}
