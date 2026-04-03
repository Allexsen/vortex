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
        el.innerHTML = `
            <div class="result-url">
                <a href="${item.url}" target="_blank" rel="noopener">${item.url}</a>
            </div>
            <div class="result-text">${truncate(item.chunk_text, 300)}</div>
            <div class="result-footer">
                <div class="relevance-bar">
                    <div class="relevance-track">
                        <div class="relevance-fill" style="width: ${relevance}%"></div>
                    </div>
                    ${relevance}%
                </div>
            </div>
        `;
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
        if (!res.ok) throw new Error(res.statusText);
        const data = await res.json();
        renderResults(data, query.trim());
    } catch (err) {
        loading.classList.remove('active');
        errorState.classList.add('active');
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
