const input = document.getElementById('searchInput');
const btn = document.getElementById('searchBtn');
const resultsContainer = document.getElementById('results');
const loading = document.getElementById('loading');
const emptyState = document.getElementById('emptyState');
const errorState = document.getElementById('errorState');
const searchWrapper = document.getElementById('searchWrapper');
const searchHero = document.getElementById('searchHero');

function toRelevance(distance) {
    // cosine distance: 0 = identical, 2 = opposite
    return Math.max(0, Math.round((1 - distance) * 100));
}

function truncate(text, maxLen) {
    if (text.length <= maxLen) return text;
    return text.substring(0, maxLen).trimEnd() + '...';
}

const errorMessages = {
    429: 'Too many requests. Please slow down.',
    400: 'Invalid search query.',
    500: 'Server error. Please try again later.',
    502: 'Server is temporarily unavailable.',
    503: 'Service is currently unavailable.',
    0:   'Could not reach the server. Check your connection.',
};

function showError(status) {
    const msg = errorMessages[status] || `Something went wrong (${status}).`;
    errorState.textContent = msg;
    errorState.classList.add('active');
}

function renderResults(data, query) {
    resultsContainer.innerHTML = '';
    loading.classList.remove('active');
    emptyState.classList.remove('active');
    errorState.classList.remove('active');

    if (!data || data.length === 0) {
        emptyState.classList.add('active');
        return;
    }

    searchWrapper.classList.add('has-results');
    searchHero.classList.add('hidden');

    const meta = document.createElement('div');
    meta.className = 'results-meta';
    meta.textContent = `${data.length} result${data.length !== 1 ? 's' : ''} for "${query}"`;
    resultsContainer.appendChild(meta);

    data.forEach(item => {
        const relevance = toRelevance(item.distance);
        const el = document.createElement('div');
        el.className = 'result';

        const urlDiv = document.createElement('div');
        urlDiv.className = 'result-url';
        const link = document.createElement('a');
        link.href = item.url.startsWith('http') ? item.url : '#';
        link.target = '_blank';
        link.rel = 'noopener';
        link.textContent = item.url;
        urlDiv.appendChild(link);

        const textDiv = document.createElement('div');
        textDiv.className = 'result-text';
        textDiv.textContent = truncate(item.chunk_text, 300);

        const footerDiv = document.createElement('div');
        footerDiv.className = 'result-footer';
        const bar = document.createElement('div');
        bar.className = 'relevance-bar';
        const track = document.createElement('div');
        track.className = 'relevance-track';
        const fill = document.createElement('div');
        fill.className = 'relevance-fill';
        fill.style.width = `${relevance}%`;
        track.appendChild(fill);
        bar.appendChild(track);
        bar.appendChild(document.createTextNode(` ${relevance}%`));
        footerDiv.appendChild(bar);

        el.appendChild(urlDiv);
        el.appendChild(textDiv);
        el.appendChild(footerDiv);
        resultsContainer.appendChild(el);
    });
}

async function doSearch(query) {
    if (!query.trim()) return;

    resultsContainer.innerHTML = '';
    emptyState.classList.remove('active');
    errorState.classList.remove('active');
    loading.classList.add('active');
    searchWrapper.classList.add('has-results');
    searchHero.classList.add('hidden');

    try {
        const res = await fetch(`/search?q=${encodeURIComponent(query.trim())}&limit=10`);
        if (!res.ok) {
            loading.classList.remove('active');
            showError(res.status);
            return;
        }
        const data = await res.json();
        renderResults(data, query.trim());
    } catch (err) {
        loading.classList.remove('active');
        showError(0);
    }
}

// Enter to search
input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') doSearch(input.value);
});

// Button click
btn.addEventListener('click', () => doSearch(input.value));

// / to focus search
document.addEventListener('keydown', (e) => {
    if (e.key === '/' && document.activeElement !== input) {
        e.preventDefault();
        input.focus();
    }
});

// Reset to centered state when input is cleared
input.addEventListener('input', () => {
    if (input.value === '') {
        searchWrapper.classList.remove('has-results');
        searchHero.classList.remove('hidden');
        resultsContainer.innerHTML = '';
        emptyState.classList.remove('active');
        errorState.classList.remove('active');
        loading.classList.remove('active');
    }
});
