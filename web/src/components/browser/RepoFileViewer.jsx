function handleFoldToggle(event) {
  const btn = event.target.closest('.fold-toggle');
  if (!btn) {
    return;
  }
  const lineNum = parseInt(btn.dataset.foldLine, 10);
  if (!lineNum) {
    return;
  }
  const table = event.currentTarget.querySelector('.code-table');
  if (!table) {
    return;
  }
  const isFolded = btn.classList.toggle('folded');
  const rows = table.querySelectorAll('tr.code-line');
  for (const row of rows) {
    const range = row.dataset.foldRange || '';
    if (!range) {
      continue;
    }
    const ranges = range.split(' ');
    for (const r of ranges) {
      const [start, end] = r.split('-').map(Number);
      if (start === lineNum) {
        const rowLine = parseInt(row.dataset.line, 10);
        if (rowLine > start && rowLine <= end) {
          row.classList.toggle('folded', isFolded);
        }
      }
    }
  }
}

export default function RepoFileViewer({
  draftContent,
  fileError,
  hasPreviewContent,
  highlightedContent,
  isEditingFile,
  isSelectedFileLoading,
  markdownContent,
  onDraftContentChange,
  previewMeta,
  previewPath,
  selectedFile,
  showHistory,
}) {
  if (!selectedFile || showHistory) {
    return null;
  }

  return (
    <>
      {!isSelectedFileLoading && fileError && <div className="panel-error">{fileError}</div>}
      {((isSelectedFileLoading && hasPreviewContent) || (!isSelectedFileLoading && !fileError)) && (
        isEditingFile ? (
          <textarea
            className="file-editor"
            value={draftContent}
            onChange={(event) => onDraftContentChange(event.target.value)}
            spellCheck={false}
          />
        ) : previewMeta.mode === 'image' ? (
          <div className="media-preview-wrapper">
            <img className="media-preview-image" src={previewMeta.src} alt={previewPath} />
          </div>
        ) : previewMeta.mode === 'pdf' ? (
          <iframe
            className="media-preview-pdf"
            src={previewMeta.src}
            title={`${previewPath} PDF preview`}
          />
        ) : previewMeta.mode === 'markdown' ? (
          <article
            className="file-preview file-preview-markdown"
            dangerouslySetInnerHTML={{ __html: markdownContent || '<p>File is empty.</p>' }}
          />
        ) : (
          <div className="file-preview" onClick={handleFoldToggle}>
            <table className="code-table" dangerouslySetInnerHTML={{ __html: highlightedContent || '<tr><td class="line-number"></td><td class="line-content">File is empty.</td></tr>' }} />
          </div>
        )
      )}
    </>
  );
}
