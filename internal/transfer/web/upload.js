"use strict";
(function () {
  var token = location.pathname.split("/")[2] || "";
  var app = document.getElementById("app");
  var expires = Number(app.dataset.expires || 0);
  var el = function (id) { return document.getElementById(id); };
  var busy = false;

  function human(n) {
    var u = ["B", "KB", "MB", "GB", "TB"], i = 0;
    while (n >= 1000 && i < u.length - 1) { n /= 1000; i++; }
    return (i === 0 ? n : n.toFixed(n >= 100 ? 0 : 1)) + " " + u[i];
  }
  function showError(msg) { var e = el("error"); e.textContent = msg; e.hidden = false; }
  function tick() {
    var left = Math.max(0, Math.round((expires - Date.now()) / 1000));
    if (left === 0 && !busy) {
      el("expiry").textContent = "This link has expired.";
      el("pick").hidden = true;
      return;
    }
    el("expiry").textContent = "Link valid for " + Math.floor(left / 60) + ":" + String(left % 60).padStart(2, "0");
    setTimeout(tick, 1000);
  }
  tick();

  function uploadOne(file, onProgress) {
    return new Promise(function (resolve, reject) {
      var form = new FormData();
      form.append("file", file, file.name);
      var xhr = new XMLHttpRequest();
      xhr.open("POST", "/u/" + encodeURIComponent(token) + "/files");
      xhr.upload.onprogress = function (e) { if (e.lengthComputable) onProgress(e.loaded); };
      xhr.onload = function () {
        var body = {};
        try { body = JSON.parse(xhr.responseText); } catch (e) {}
        if (xhr.status === 200 && body.saved && body.saved.length) resolve(body.saved[0]);
        else reject(new Error(body.error || "Upload failed (" + xhr.status + ")"));
      };
      xhr.onerror = function () { reject(new Error("Connection lost. Is your phone on the same Wi-Fi as the Vault computer?")); };
      xhr.send(form);
    });
  }

  async function run(files) {
    if (!files.length) return;
    busy = true;
    el("error").hidden = true;
    el("pick").hidden = true;
    el("done").hidden = true;
    el("progress").hidden = false;
    var list = el("list");
    list.innerHTML = "";
    var total = 0, sent = 0, ok = 0, failed = 0, savedNames = [];
    files.forEach(function (f) { total += f.size; });
    var rows = files.map(function (f) {
      var li = document.createElement("li");
      var a = document.createElement("span"); a.textContent = f.name;
      var b = document.createElement("span"); b.className = "muted"; b.textContent = human(f.size);
      li.appendChild(a); li.appendChild(b); list.appendChild(li);
      return b;
    });
    for (var i = 0; i < files.length; i++) {
      var base = sent;
      el("status").textContent = "Uploading " + (i + 1) + " of " + files.length + "…";
      try {
        var saved = await uploadOne(files[i], function (loaded) {
          el("bar").style.width = (total ? ((base + loaded) / total) * 100 : 100) + "%";
          el("counts").textContent = human(base + loaded) + " of " + human(total);
        });
        ok++; savedNames.push(saved.name);
        rows[i].textContent = "✓ " + human(saved.size); rows[i].className = "ok";
      } catch (err) {
        failed++;
        rows[i].textContent = "✕"; rows[i].className = "bad";
        showError(err.message);
        if (/expired|stopped|full|not valid/i.test(err.message)) break;
      }
      sent += files[i].size;
    }
    el("bar").style.width = "100%";
    busy = false;
    el("progress").hidden = true;
    el("done").hidden = false;
    el("summary").textContent = ok + (ok === 1 ? " file" : " files") + " uploaded" + (failed ? ", " + failed + " failed" : "") + ".";
    var doneList = document.createElement("ul");
    savedNames.forEach(function (n) { var li = document.createElement("li"); li.textContent = n; doneList.appendChild(li); });
    var old = el("done").querySelector("ul"); if (old) old.remove();
    el("summary").after(doneList);
  }

  document.querySelectorAll("input[type=file]").forEach(function (input) {
    input.addEventListener("change", function () {
      var files = Array.prototype.slice.call(input.files || []);
      input.value = "";
      run(files);
    });
  });
  el("more").addEventListener("click", function () {
    el("done").hidden = true;
    el("pick").hidden = false;
    el("error").hidden = true;
  });
})();
