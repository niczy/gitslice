import { useEffect, useMemo, useRef, useState } from 'react';
import { Check, ChevronDown, Database, Search } from 'lucide-react';
import { getSliceDisplayName } from '../utils/slices.js';
import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Input } from './ui/input.jsx';

// ---------------------------------------------------------------------------
// Slice Dropdown Component
// ---------------------------------------------------------------------------

export default function SliceDropdown({
  slices,
  currentSliceId,
  onSelectSlice,
  loading,
  error,
  onRefresh,
  className = '',
}) {
  const [isOpen, setIsOpen] = useState(false);
  const [filter, setFilter] = useState('');
  const dropdownRef = useRef(null);

  const currentSlice = useMemo(() =>
    slices.find((s) => s.slice_id === currentSliceId),
    [slices, currentSliceId]
  );

  const currentSliceLabel = useMemo(() => {
    if (currentSlice) {
      return getSliceDisplayName(currentSlice.name || currentSlice.slice_id);
    }
    if (String(currentSliceId || '').startsWith('home.')) {
      return getSliceDisplayName(String(currentSliceId).slice('home.'.length));
    }
    return getSliceDisplayName(currentSliceId);
  }, [currentSlice, currentSliceId]);

  const currentSliceMeta = useMemo(() => {
    if (!currentSlice) {
      return currentSliceId ? 'Requested slice' : 'Choose workspace';
    }
    if (currentSlice.is_root) {
      return 'Root collection';
    }
    if (currentSlice.slug) {
      return currentSlice.slug;
    }
    return currentSlice.slice_id || 'Workspace slice';
  }, [currentSlice, currentSliceId]);

  const filteredSlices = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return slices;
    return slices.filter((slice) => {
      const name = (slice.name || slice.slice_id || '').toLowerCase();
      const slug = (slice.slug || '').toLowerCase();
      const description = (slice.description || '').toLowerCase();
      return name.includes(query) || slug.includes(query) || description.includes(query);
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
      <Button
        type="button"
        variant="outline"
        className="slice-dropdown-trigger"
        onClick={() => setIsOpen(!isOpen)}
        data-testid="slice-dropdown-trigger"
      >
        <span className="slice-dropdown-trigger-icon" aria-hidden="true">
          <Database size={16} />
        </span>
        <span className="slice-dropdown-trigger-copy">
          <span className="slice-dropdown-trigger-label">{currentSliceLabel || 'Select slice'}</span>
          <span className="slice-dropdown-trigger-meta">{currentSliceMeta}</span>
        </span>
        <ChevronDown className={`slice-dropdown-arrow${isOpen ? ' open' : ''}`} size={16} aria-hidden="true" />
      </Button>
      {isOpen && (
        <div className="slice-dropdown-menu" data-testid="slice-dropdown-menu">
          <div className="slice-dropdown-header">
            <h4>Slices</h4>
            <div className="slice-search">
              <Search size={15} aria-hidden="true" />
              <Input
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
                <Button type="button" variant="ghost" size="sm" onClick={onRefresh} style={{ marginLeft: '8px' }}>
                  Retry
                </Button>
              </li>
            )}
            {!loading && !error && filteredSlices.length === 0 && (
              <li className="slice-dropdown-empty">No slices found.</li>
            )}
            {!loading && !error && filteredSlices.map((slice) => (
              <li key={slice.slice_id}>
                <Button
                  type="button"
                  variant="ghost"
                  className={`slice-dropdown-item w-full justify-start ${currentSliceId === slice.slice_id ? 'active' : ''}`}
                  onClick={() => handleSelect(slice.slice_id)}
                  data-testid="slice-dropdown-item"
                >
                  <div className="slice-dropdown-item-title">
                    <span className="slice-name">{getSliceDisplayName(slice.name || slice.slice_id)}</span>
                    {slice.is_root && <Badge variant="outline" className="slice-badge">root</Badge>}
                    {currentSliceId === slice.slice_id && <Check size={15} aria-hidden="true" />}
                  </div>
                  <div className="slice-dropdown-item-meta">
                    <span>{slice.slug || slice.slice_id}</span>
                    {typeof slice.file_count === 'number' && (
                      <span>{slice.file_count} files</span>
                    )}
                  </div>
                  {slice.description && (
                    <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '2px' }}>
                      {slice.description}
                    </div>
                  )}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
