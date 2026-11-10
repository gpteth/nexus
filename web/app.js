// Nexus landing page interactions
const REPO = "gpteth/nexus";

// ---- GitHub star count ----
async function loadStars() {
  try {
    const res = await fetch(`https://api.github.com/repos/${REPO}`, {
      headers: { Accept: "application/vnd.github+json" },
    });
    if (!res.ok) throw new Error("GitHub API failed");
    const data = await res.json();
    const n = data.stargazers_count || 0;
    const formatted = n >= 1000 ? (n / 1000).toFixed(1) + "k" : String(n);
    const c1 = document.getElementById("starCount");
    const c2 = document.getElementById("starCount2");
    if (c1) c1.textContent = "★ " + formatted;
    if (c2) c2.textContent = formatted;
  } catch (e) {
    const c1 = document.getElementById("starCount");
    if (c1) c1.textContent = "Star";
  }
}

// ---- Mobile menu ----
const nav = document.querySelector(".nav");
const toggle = document.getElementById("menuToggle");
if (toggle) {
  toggle.addEventListener("click", () => nav.classList.toggle("open"));
}
document.querySelectorAll(".nav-links a").forEach((a) =>
  a.addEventListener("click", () => nav.classList.remove("open"))
);

// ---- Waitlist form (client-side capture; wire to a backend/CRM later) ----
const form = document.getElementById("waitlistForm");
if (form) {
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const email = new FormData(form).get("email");
    try {
      const list = JSON.parse(localStorage.getItem("nexus_waitlist") || "[]");
      list.push(email);
      localStorage.setItem("nexus_waitlist", JSON.stringify(list));
    } catch (_) {
      /* storage may be unavailable; still show confirmation */
    }
    form.hidden = true;
    const note = document.getElementById("formNote");
    if (note) note.hidden = false;
  });
}

loadStars();
