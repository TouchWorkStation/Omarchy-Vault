// Shows how long a download or share link stays valid.
(function () {
  "use strict";
  var app = document.getElementById("app");
  var out = document.getElementById("expiry");
  if (!app || !out) return;
  var expires = Number(app.dataset.expires || 0);
  function tick() {
    var left = Math.max(0, Math.round((expires - Date.now()) / 1000));
    if (left === 0) {
      out.textContent = "This link has expired.";
      return;
    }
    var h = Math.floor(left / 3600), m = Math.floor((left % 3600) / 60), s = left % 60;
    if (h >= 48) out.textContent = "Link valid for " + Math.floor(h / 24) + " days";
    else if (h > 0) out.textContent = "Link valid for " + h + " h " + m + " min";
    else out.textContent = "Link valid for " + m + ":" + String(s).padStart(2, "0");
    setTimeout(tick, 1000);
  }
  tick();
})();
