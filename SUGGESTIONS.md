# Suggestions

## Incremental indexing resets status to "starting"

Când se re-indexează incremental un singur fișier, `StartIndexingAsync` suprascrie statusul la `state: "starting"` cu totul de la zero, ștergând informația că 99% din index e deja acolo și funcțional. AI-ul vede `"starting"` + `"processed": 0` și crede că nu are date.

Fix-ul corect ar fi: la indexare incrementală, nu reseta starea la `"starting"` — folosește ceva gen `"updating"` sau păstrează `"completed"` cu un sub-status. Dar asta e un issue separat, nu din PR review-ul curent.
