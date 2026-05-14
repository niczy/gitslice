import { Globe2, Lock, Search } from 'lucide-react';
import { formatTimestamp } from '../../utils/format.js';
import { Button } from '../ui/button.jsx';
import {
  getSliceMeta,
  getSliceName,
  getSliceRouteRef,
  getSliceUpdatedAt,
  getSliceVisibility,
  isHomeSlice,
} from './SliceHomeHelpers.js';

export function SliceHomeList({
  filteredSlices,
  homeSliceId,
  onOpenSlice,
  query,
  setQuery,
  slicesError,
  slicesLoading,
}) {
  return (
    <>
      <div className="slice-home-toolbar">
        <label className="slice-home-search">
          <Search size={16} aria-hidden="true" />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search slices"
            data-testid="slice-home-search"
          />
        </label>
      </div>

      <div className="slice-home-panel">
        {slicesLoading && (
          <div className="slice-home-list" data-testid="slice-home-loading">
            {[0, 1, 2].map((item) => (
              <div className="slice-home-skeleton" key={item} />
            ))}
          </div>
        )}
        {!slicesLoading && slicesError && <div className="panel-error">{slicesError}</div>}
        {!slicesLoading && !slicesError && filteredSlices.length === 0 && (
          <div className="panel-empty" data-testid="slice-home-empty">
            No slices match this search.
          </div>
        )}
        {!slicesLoading && !slicesError && filteredSlices.length > 0 && (
          <ul className="slice-home-list" data-testid="slice-home-list">
            {filteredSlices.map((slice) => {
              const updatedAt = getSliceUpdatedAt(slice);
              const isHome = isHomeSlice(slice, homeSliceId);
              const visibility = getSliceVisibility(slice);
              return (
                <li key={slice.slice_id}>
                  <Button
                    type="button"
                    variant="ghost"
                    className={`slice-home-row${isHome ? ' slice-home-row--home' : ''}`}
                    onClick={() => onOpenSlice(getSliceRouteRef(slice, homeSliceId))}
                    data-testid="slice-home-row"
                  >
                    <span className="slice-home-row-main">
                      <span className="slice-home-row-title">{getSliceName(slice)}</span>
                      <span className="slice-home-row-subtitle">{getSliceMeta(slice)}</span>
                    </span>
                    <span className="slice-home-row-updated">
                      {updatedAt ? formatTimestamp(updatedAt) : 'No updates yet'}
                    </span>
                    <span className={`slice-home-chip slice-home-chip--visibility slice-home-chip--${visibility}`}>
                      {visibility === 'public' ? (
                        <Globe2 size={13} aria-hidden="true" />
                      ) : (
                        <Lock size={13} aria-hidden="true" />
                      )}
                      {visibility === 'public' ? 'Public' : 'Private'}
                    </span>
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </>
  );
}
