// Navbar active link highlight - sets background on the nav link matching the current path.
(function () {
    // match the current path to a nav link ID
    const path = window.location.pathname;
    let active = null;
    if (path === '/' || path.startsWith('/package')) active = 'nav-packages';
    else if (path.startsWith('/channel')) active = 'nav-channels';
    else if (path.startsWith('/log')) active = 'nav-log';
    else if (path.startsWith('/admin/config')) active = 'nav-sysconfig';

    // apply highlight to matched link
    if (active) {
        var el = document.getElementById(active);
        if (el) {
            el.style.background = '#0090CE';
            el.style.border = '1px solid #21BCFF';
        }
    }
})();

// Bootstrap tooltip initialisation - runs on page load and after every HTMX swap
(function () {
    // initialise any uninitialised tooltips in given root element
    function initTooltips(root) {
        root.querySelectorAll('[data-bs-toggle="tooltip"]').forEach(el => {
            // skip elements that already have a tooltip
            if (!bootstrap.Tooltip.getInstance(el)) {
                new bootstrap.Tooltip(el);
            }
        });
    }
    // run once on page load
    initTooltips(document);
    // run on every HTMX swap so tooltips work on dynamically inserted elements
    document.body.addEventListener("htmx:afterSwap", function (event) {
        initTooltips(event.target);
    });
    // clean up stuck tooltips before every HTMX swap
    document.body.addEventListener("htmx:beforeSwap", function () {
        document.querySelectorAll('.tooltip').forEach(el => el.remove());
    });
})();

// Mattermost details expand/collapse - toggles the details panel and chevron icon on click
(function () {
    document.addEventListener("click", function (event) {
        // ignore clicks that are not in .js-channel-expand button
        const btn = event.target.closest(".js-channel-expand");
        if (!btn) return;
        // find the details panel this button controls
        const details = document.getElementById(btn.dataset.detailsId);
        if (!details) return;
        // toggle details panel visibility
        const isHidden = details.classList.contains("d-none");
        details.classList.toggle("d-none");
        // add .expanded to the main row when open so it has a shadow style
        const mainRow = btn.closest(".js-channel-main-row");
        if (mainRow) {
            mainRow.classList.toggle("expanded", isHidden);
        }
        // swap chevron direction
        const icon = btn.querySelector("i");
        if (!icon) return;
        icon.classList.toggle("bi-arrow-down-circle");
        icon.classList.toggle("bi-arrow-up-circle");
    });
})();

// Details panel expand/collapse - handles both channel (Mattermost) and log (error) rows
(function () {
    document.addEventListener("click", function (event) {
        // ignore clicks that are not in .js-expand button
        const btn = event.target.closest(".js-expand");
        if (!btn) return;
        // find the details panel this button controls
        const details = document.getElementById(btn.dataset.detailsId);
        if (!details) return;
        // toggle details panel visibility
        const isHidden = details.classList.contains("d-none");
        details.classList.toggle("d-none");
        // apply shadow to the nearest main row when expanded
        const mainRow = btn.closest(".js-expand-main-row");
        if (mainRow) {
            mainRow.classList.toggle("expanded", isHidden);
        }
        // swap chevron direction
        const icon = btn.querySelector("i");
        if (!icon) return;
        icon.classList.toggle("bi-arrow-down-circle");
        icon.classList.toggle("bi-arrow-up-circle");
    });
})();

// New channel form type selector - shows/hides Mattermost specific fields when the type dropdown changes
(function () {
    function handleTypeChange(select) {
        const mm = select.value === 'webhook_mattermost';
        const form = select.closest('form');
        const suffix = form.dataset.formId;

        const username = document.getElementById('mm-username-' + suffix);
        const col2 = document.getElementById('mm-fields-col2-' + suffix);
        if (!username || !col2) return;

        username.classList.toggle('d-none', !mm);
        if (mm) {
            col2.classList.remove('d-none');
            col2.classList.add('d-flex');
            form.classList.remove('rounded-5');
            form.classList.add('rounded-4-5');
        } else {
            col2.classList.remove('d-flex');
            col2.classList.add('d-none');
            if (!form.dataset.errorForm) {
                form.classList.remove('rounded-4-5');
                form.classList.add('rounded-5');
            }
        }
    }

    document.body.addEventListener('change', function (e) {
        const select = e.target.closest('select[name="type"]');
        if (!select) return;
        handleTypeChange(select);
    });
})();

// Profile menu toggle and username editor (handles profile menu dropdown open/close and inline rename flow)
(function () {
    const toggle = document.getElementById("profile-toggle");
    const menu = document.getElementById("profile-menu");
    const form = document.getElementById("profile-name-form");
    const cancelBtn = document.getElementById("profile-name-cancel-btn");
    const input = document.getElementById("profile-name-input");

    if (!toggle || !menu) return;

    // resets username editor back to display mode
    // called on menu close, Cancel click or after a successful HTMX rename swap
    function resetProfileNameEditor() {
        const display = document.getElementById("profile-name-display");
        const err = document.getElementById("profile-name-error");
        if (!display || !form || !input) return;
        form.classList.add("d-none");
        display.classList.remove("d-none");
        input.value = input.defaultValue;
        if (err) { err.textContent = ""; err.classList.add("d-none"); }
    }

    // --- Dropdown toggle ---

    function closeProfileMenu() {
        menu.classList.add("d-none");
        resetProfileNameEditor();
    }

    // toggle open/closed on button click
    toggle.addEventListener("click", function (event) {
        event.stopPropagation(); // stop propagation so the document click below doesn't immediately close it
        const isHidden = menu.classList.contains("d-none");
        if (isHidden) {
            menu.classList.remove("d-none");
            toggle.blur();
        } else {
            closeProfileMenu();
        }
    });

    // stop clicks inside the profile menu propagate to document close handler
    menu.addEventListener("click", function (event) {
        event.stopPropagation();
    });

    // close profile menu on outside click (only when menu is actually visible)
    document.addEventListener("click", function () {
        if (!menu.classList.contains("d-none")) {
            closeProfileMenu();
        }
    });

    // close profile menu on Escape (prevent default to stop browser moving focus)
    document.addEventListener("keydown", function (event) {
        if (event.key === "Escape") {
            event.preventDefault();
            closeProfileMenu();
        }
    });

    // --- Username editor inside profile menu ---

    if (!form || !cancelBtn || !input) return;

    // open the editor when pencil button inside #profile-name-display is clicked
    menu.addEventListener("click", function (event) {
        if (!event.target.closest("#profile-name-edit-btn")) return;
        const display = document.getElementById("profile-name-display");
        if (!display) return;
        display.classList.add("d-none");
        form.classList.remove("d-none");
        input.focus();
        input.select();
    });

    // Cancel button resets editor back to display mode
    cancelBtn.addEventListener("click", function (event) {
        event.stopPropagation();
        resetProfileNameEditor();
    });

    // hide editor after HTMX successfully swaps #profile-name-display on rename
    document.body.addEventListener("htmx:afterSwap", function (event) {
        if (event.detail.target.id !== "profile-name-display") return;
        resetProfileNameEditor();
    });

    // show error below input when a rename request fails
    document.body.addEventListener("htmx:responseError", function (event) {
        if (!event.detail.elt.closest("#profile-name-form")) return;
        const err = document.getElementById("profile-name-error");
        if (!err) return;
        err.textContent = event.detail.xhr.responseText || "Failed to update username.";
        err.classList.remove("d-none");
    });
})();

// Attach CSRF token to every HTMX request as X-CSRF-Token header so it can be validated by nosurf.
// Token is read from data-csrf on <body>.
document.body.addEventListener("htmx:configRequest", function (e) {
    e.detail.headers["X-CSRF-Token"] = document.body.dataset.csrf;
});