package ui

// The four panel helpers below are drawn from BOTH the theme's design canvas and
// AsyncAO's own chrome, and each caller declares which it is. Getting that
// boolean wrong is invisible to every other test in this package: the ink still
// resolves, the panel still draws, and the only symptom is a theme's black
// track-list colour bleeding onto a floating window that fills its own dark
// panel — or, in the other direction, a theme's colours silently doing nothing.
//
// So this gate reads the call sites as SOURCE. It is the same shape as
// TestEveryHandDrawnKeyHasAPainter, and for the same reason: the invariant lives
// in which file the call is written in, which no runtime assertion can see.
