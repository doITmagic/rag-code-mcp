package index

// isBusy reports whether a file could not be read because another process
// holds it locked (a Windows sharing or lock violation: an antivirus scan, a
// sync client, an editor's save), which goes away by itself. A var so tests
// on systems without such locks can fake one.
var isBusy = fileBusy
