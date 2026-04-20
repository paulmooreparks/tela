// sidebar-version.js
//
// Renders a small footer at the bottom of the mdBook sidebar showing the
// book's version and an edition switcher to the other channels.
//
// The __TELA_BOOK_VERSION__ and __TELA_BOOK_CHANNEL__ placeholders are
// substituted by .github/workflows/docs.yml at build time. For local
// `mdbook serve` these placeholders render as literal strings, which is
// acceptable; local authors do not see the version UI.

document.addEventListener('DOMContentLoaded', function () {
    var scrollbox = document.querySelector('.sidebar-scrollbox');
    if (!scrollbox) return;

    var version = '__TELA_BOOK_VERSION__';
    var channel = '__TELA_BOOK_CHANNEL__';

    var footer = document.createElement('div');
    footer.className = 'sidebar-version-footer';

    var versionLine = document.createElement('div');
    versionLine.className = 'sidebar-version-line';
    versionLine.textContent = version;
    footer.appendChild(versionLine);

    var switcher = document.createElement('div');
    switcher.className = 'sidebar-edition-switcher';

    var label = document.createElement('span');
    label.className = 'sidebar-edition-label';
    label.textContent = 'Other editions: ';
    switcher.appendChild(label);

    var links = [
        { name: 'stable',  href: '/tela/' },
        { name: 'beta',    href: '/tela/beta/' },
        { name: 'dev',     href: '/tela/dev/' },
        { name: 'archive', href: '/tela/archive/' }
    ];

    var first = true;
    links.forEach(function (link) {
        if (link.name === channel) return;
        if (!first) {
            var sep = document.createElement('span');
            sep.className = 'sidebar-edition-sep';
            sep.textContent = ' \u00b7 ';
            switcher.appendChild(sep);
        }
        var a = document.createElement('a');
        a.href = link.href;
        a.textContent = link.name;
        a.className = 'sidebar-edition-link';
        switcher.appendChild(a);
        first = false;
    });

    footer.appendChild(switcher);
    scrollbox.appendChild(footer);
});
