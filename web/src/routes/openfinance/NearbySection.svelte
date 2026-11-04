<script lang="ts">
  import Card from '../../components/Card.svelte';
  import Problem from '../../components/Problem.svelte';
  import { apiGet } from '../../lib/api';
  import { parseLatLon } from '../../lib/format';
  import type { OpenDataList, OpenDataRecord, PlaceData } from '../../lib/types';
  import Badges from './Badges.svelte';

  const LONDON = '51.5074,-0.1278';

  let kind = $state<'atms' | 'branches'>('atms');
  let point = $state(LONDON);
  let pointError = $state('');
  let locating = $state(false);
  let records = $state<OpenDataRecord<PlaceData>[] | null>(null);
  let error = $state<unknown>(null);
  let loading = $state(false);

  async function search() {
    const p = parseLatLon(point);
    pointError = p ? '' : 'Enter latitude,longitude, e.g. 51.5074,-0.1278';
    if (!p) return;
    error = null;
    loading = true;
    try {
      const q = `${p.lat},${p.lon}`;
      records = (await apiGet<OpenDataList<PlaceData>>(`/api/opendata/${kind}?near=${encodeURIComponent(q)}&limit=20`)).records;
    } catch (e) {
      error = e;
    } finally {
      loading = false;
    }
  }

  function useMyLocation() {
    if (!('geolocation' in navigator)) {
      pointError = 'Location is not available in this browser. Enter coordinates instead.';
      return;
    }
    locating = true;
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        locating = false;
        point = `${pos.coords.latitude.toFixed(5)},${pos.coords.longitude.toFixed(5)}`;
        search();
      },
      () => {
        locating = false;
        pointError = 'Could not get your location. Enter coordinates instead.';
      },
      { enableHighAccuracy: false, timeout: 10_000, maximumAge: 300_000 },
    );
  }

  function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    search();
  }

  search();

  const km = (d: number) => `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(d)} km`;
  const mapUrl = (p: PlaceData) => `https://www.openstreetmap.org/?mlat=${p.lat}&mlon=${p.lon}#map=17/${p.lat}/${p.lon}`;
</script>

<Card title="Nearby ATMs and branches" id="nearby">
  <form onsubmit={onSubmit} novalidate>
    <fieldset class="kind">
      <legend>Show</legend>
      <label class="inline"><input type="radio" bind:group={kind} value="atms" onchange={search} /> ATMs</label>
      <label class="inline"><input type="radio" bind:group={kind} value="branches" onchange={search} /> Branches</label>
    </fieldset>
    <div class="field">
      <label for="near">Near (latitude,longitude)</label>
      <div class="near-row">
        <input
          id="near"
          bind:value={point}
          inputmode="decimal"
          autocomplete="off"
          aria-invalid={pointError ? 'true' : undefined}
          aria-describedby="near-hint{pointError ? ' near-error' : ''}"
        />
        <button type="submit" disabled={loading}>Search</button>
      </div>
      <p class="hint" id="near-hint">Defaults to central London. Your location is only sent to this bank's server to sort results.</p>
      {#if pointError}<p class="field-error" id="near-error">{pointError}</p>{/if}
    </div>
    <button type="button" class="secondary" onclick={useMyLocation} disabled={locating} aria-busy={locating}>
      {locating ? 'Locating…' : 'Use my location'}
    </button>
  </form>

  <div class="results" aria-busy={loading}>
    {#if error}
      <Problem {error} onretry={search} />
    {:else if records === null}
      <p aria-busy="true">Loading…</p>
    {:else if records.length === 0}
      <p class="muted">Nothing found near this point.</p>
    {:else}
      <Badges {records} />
      <ol class="list-plain places">
        {#each records as r (r.key)}
          <li>
            <div class="place-top">
              <strong>{r.data.name || r.data.bank}</strong>
              <span>{km(r.data.distance_km)}</span>
            </div>
            <p class="muted">{r.data.bank} · {[r.data.address, r.data.postcode].filter(Boolean).join(', ')}</p>
            <p class="row">
              {#if r.data.access24h}<span class="pill ok">24 hours</span>{/if}
              <a href={mapUrl(r.data)} rel="noopener noreferrer" target="_blank">Map<span class="visually-hidden"> for {r.data.name} (opens in a new tab)</span></a>
            </p>
          </li>
        {/each}
      </ol>
    {/if}
  </div>
</Card>

<style>
  fieldset {
    border: 0;
    padding: 0;
    margin: 0 0 var(--space-3);
  }
  legend {
    font-weight: 600;
    padding: 0;
  }
  label.inline {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    font-weight: 400;
    min-height: 44px;
    margin-right: var(--space-4);
  }
  label.inline input {
    width: 20px;
    height: 20px;
    min-height: 0;
  }
  .near-row {
    display: flex;
    gap: var(--space-2);
  }
  .near-row input {
    flex: 1;
    min-width: 0;
  }
  .results {
    margin-top: var(--space-4);
  }
  .places li {
    padding: var(--space-3) 0;
    border-bottom: 1px solid var(--color-border-subtle);
  }
  .places p {
    margin: var(--space-1) 0 0;
    font-size: var(--text-sm);
  }
  .place-top {
    display: flex;
    justify-content: space-between;
    gap: var(--space-2);
  }
  .place-top strong {
    min-width: 0;
    overflow-wrap: anywhere;
  }
</style>
