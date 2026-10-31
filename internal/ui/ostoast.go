package ui

import "time"

// osToastMinInterval rate-limits only the OS popup, so a chatty friend can't storm the notification centre.
const osToastMinInterval = 4 * time.Second
