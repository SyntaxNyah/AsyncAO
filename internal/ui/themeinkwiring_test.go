package ui

// The four panel helpers below are drawn from BOTH the theme's design canvas and
// AsyncAO's own chrome, and each caller declares which it is. Getting that
// boolean wrong is invisible to every other test in this package: the ink still
// resolves, the panel still draws, and the only symptom is a theme's black
// track-list colour bleeding onto a floating window that fills its own dark
// panel — or, in the other direction, a theme's colours silently doing nothing.
