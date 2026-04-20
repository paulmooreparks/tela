document.addEventListener('DOMContentLoaded', function () {
    var scrollbox = document.querySelector('.sidebar-scrollbox');
    if (!scrollbox) return;
    var footer = document.createElement('div');
    footer.className = 'sidebar-version-footer';
    footer.textContent = 'v0.12.0';
    scrollbox.appendChild(footer);
});
