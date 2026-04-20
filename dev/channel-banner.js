// channel-banner.js
//
// Injects a banner at the top of every page on non-stable editions of
// the book, telling the reader which channel they are on and linking to
// the current stable edition.
//
// The dev and v0.14.0-dev.2 placeholders are
// substituted by .github/workflows/docs.yml at build time. For local
// `mdbook serve` the placeholders render literally, so the banner does
// not appear (the channel check fails).

(function () {
    var channel = 'dev';
    var version = 'v0.14.0-dev.2';

    // Sentinel built by string concatenation so sed does not rewrite it.
    // This detects local `mdbook serve` builds where placeholders were
    // never substituted, and skips the banner silently.
    var unsubstituted = '__TELA' + '_BOOK_CHANNEL__';
    if (channel === unsubstituted) return;
    if (channel === 'stable') return;

    function insertBanner() {
        var content = document.querySelector('main') || document.querySelector('.content');
        if (!content) return;
        if (content.querySelector('.channel-banner')) return;

        var banner = document.createElement('div');
        banner.className = 'channel-banner channel-banner-' + channel;

        var stableLink = '<a href="/tela/">paulmooreparks.github.io/tela</a>';
        var message = '';

        if (channel === 'beta') {
            message =
                'You are reading the <strong>beta</strong> documentation for Tela ' + version + '. ' +
                'The current stable release is documented at ' + stableLink + '.';
        } else if (channel === 'dev') {
            message =
                'You are reading the <strong>dev</strong> documentation for Tela ' + version + '. ' +
                'This describes behavior that has not yet shipped in a release. ' +
                'The current stable release is documented at ' + stableLink + '.';
        } else if (channel === 'archive') {
            message =
                'You are reading archived documentation for Tela ' + version + ', a previous stable release. ' +
                'The current stable release is documented at ' + stableLink + '.';
        } else {
            return;
        }

        banner.innerHTML = message;
        content.insertBefore(banner, content.firstChild);
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', insertBanner);
    } else {
        insertBanner();
    }
})();
