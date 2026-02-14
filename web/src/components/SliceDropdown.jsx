import { useEffect, useMemo, useRef, useState } from 'react';

// ---------------------------------------------------------------------------
// Slice Dropdown Component
// ---------------------------------------------------------------------------

export default function SliceDropdown({ slices, currentSliceId, onSelectSlice, loading, error, onRefresh, className = '' }) {
  const [isOpen, setIsOpen] = useState(false);
  const [filter, setFilter] = useState('');
  const dropdownRef = useRef(null);

  const currentSlice = useMemo(() =>
    slices.find((s) => s.slice_id === currentSliceId),
    [slices, currentSliceId]
  );

  const filteredSlices = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return slices;
    return slices.filter((slice) => {
      const name = (slice.name || slice.slice_id || '').toLowerCase();
      const description = (slice.description || '').toLowerCase();
      return name.includes(query) || description.includes(query);
    });
  }, [filter, slices]);

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target)) {
        setIsOpen(false);
      }
    };
    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
    }
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [isOpen]);

  const handleSelect = (sliceId) => {
    onSelectSlice(sliceId);
    setIsOpen(false);
    setFilter('');
  };

  return (
    <div className={`slice-dropdown ${className}`.trim()} ref={dropdownRef}>
      <button
        type="button"
        className="slice-dropdown-trigger"
        onClick={() => setIsOpen(!isOpen)}
        data-testid="slice-dropdown-trigger"
      >
        <span>{currentSlice ? (currentSlice.name || currentSlice.slice_id) : 'Select slice'}</span>
        <span className="slice-dropdown-arrow">{isOpen ? '▲' : '▼'}</span>
      </button>
      {isOpen && (
        <div className="slice-dropdown-menu" data-testid="slice-dropdown-menu">
          <div className="slice-dropdown-header">
            <h4>Slices</h4>
            <div className="slice-search">
              <input
                type="text"
                placeholder="Filter slices..."
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                data-testid="slice-dropdown-filter"
                autoFocus
              />
            </div>
          </div>
          <ul className="slice-dropdown-list" data-testid="slice-list">
            {loading && (
              <li className="slice-dropdown-loading">Loading…</li>
            )}
            {error && (
              <li className="slice-dropdown-error">
                {error}
                <button type="button" onClick={onRefresh} style={{ marginLeft: '8px' }}>
                  Retry
                </button>
              </li>
            )}
            {!loading && !error && filteredSlices.length === 0 && (
              <li className="slice-dropdown-empty">No slices found.</li>
            )}
            {!loading && !error && filteredSlices.map((slice) => (
              <li key={slice.slice_id}>
                <button
                  type="button"
                  className={`slice-dropdown-item ${currentSliceId === slice.slice_id ? 'active' : ''}`}
                  onClick={() => handleSelect(slice.slice_id)}
                  data-testid="slice-dropdown-item"
                >
                  <div className="slice-dropdown-item-title">
                    <span className="slice-name">{slice.name || slice.slice_id}</span>
                    {slice.is_root && <span className="slice-badge">root</span>}
                  </div>
                  <div className="slice-dropdown-item-meta">
                    <span>{slice.slice_id}</span>
                    {typeof slice.file_count === 'number' && (
                      <span>{slice.file_count} files</span>
                    )}
                  </div>
                  {slice.description && (
                    <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '2px' }}>
                      {slice.description}
                    </div>
                  )}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
