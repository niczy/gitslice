// ---------------------------------------------------------------------------
// Diff rendering utilities
// ---------------------------------------------------------------------------

export function renderDiffPatch(patchText) {
  return patchText.split('\n').map((line, index) => {
    const className = line.startsWith('+') && !line.startsWith('+++')
      ? 'diff-line-added'
      : line.startsWith('-') && !line.startsWith('---')
        ? 'diff-line-deleted'
        : line.startsWith('@@')
          ? 'diff-line-hunk'
          : line.startsWith('---') || line.startsWith('+++')
            ? 'diff-line-file'
            : 'diff-line-context';
    return (
      <span key={`${index}-${line}`} className={`diff-line ${className}`}>
        {line || ' '}
        {'\n'}
      </span>
    );
  });
}

export function parseSplitDiffLines(patchText) {
  const lines = patchText.split('\n');
  const rows = [];
  let leftNum = 0;
  let rightNum = 0;
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // File header lines (--- / +++)
    if (line.startsWith('---') || line.startsWith('+++')) {
      rows.push({ type: 'header', left: line, right: '', leftNum: null, rightNum: null });
      i++;
      continue;
    }

    // Hunk header
    if (line.startsWith('@@')) {
      const match = line.match(/@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      if (match) {
        leftNum = parseInt(match[1], 10);
        rightNum = parseInt(match[2], 10);
      }
      rows.push({ type: 'hunk', left: line, right: '', leftNum: null, rightNum: null });
      i++;
      continue;
    }

    // Collect consecutive deletions and additions for pairing
    if (line.startsWith('-')) {
      const deletions = [];
      while (i < lines.length && lines[i].startsWith('-') && !lines[i].startsWith('---')) {
        deletions.push(lines[i]);
        i++;
      }
      const additions = [];
      while (i < lines.length && lines[i].startsWith('+') && !lines[i].startsWith('+++')) {
        additions.push(lines[i]);
        i++;
      }

      const maxLen = Math.max(deletions.length, additions.length);
      for (let j = 0; j < maxLen; j++) {
        const del = j < deletions.length ? deletions[j] : null;
        const add = j < additions.length ? additions[j] : null;
        rows.push({
          type: del && add ? 'changed' : del ? 'deleted' : 'added',
          left: del ? del.slice(1) : '',
          right: add ? add.slice(1) : '',
          leftNum: del ? leftNum++ : null,
          rightNum: add ? rightNum++ : null,
          leftClass: del ? 'diff-line-deleted' : 'diff-line-empty',
          rightClass: add ? 'diff-line-added' : 'diff-line-empty',
        });
      }
      continue;
    }

    // Pure additions (not preceded by deletions)
    if (line.startsWith('+')) {
      rows.push({
        type: 'added',
        left: '',
        right: line.slice(1),
        leftNum: null,
        rightNum: rightNum++,
        leftClass: 'diff-line-empty',
        rightClass: 'diff-line-added',
      });
      i++;
      continue;
    }

    // Context lines
    if (line.length > 0 || (i < lines.length - 1)) {
      const content = line.startsWith(' ') ? line.slice(1) : line;
      rows.push({
        type: 'context',
        left: content,
        right: content,
        leftNum: leftNum++,
        rightNum: rightNum++,
        leftClass: 'diff-line-context',
        rightClass: 'diff-line-context',
      });
    }
    i++;
  }
  return rows;
}

export function renderSplitDiffPatch(patchText) {
  const rows = parseSplitDiffLines(patchText);
  return (
    <table className="diff-split-table">
      <tbody>
        {rows.map((row, idx) => {
          if (row.type === 'header') {
            return (
              <tr key={idx} className="diff-split-row diff-split-header">
                <td className="diff-split-gutter"></td>
                <td className="diff-split-cell diff-line-file" colSpan={3}>{row.left}</td>
              </tr>
            );
          }
          if (row.type === 'hunk') {
            return (
              <tr key={idx} className="diff-split-row diff-split-hunk">
                <td className="diff-split-gutter"></td>
                <td className="diff-split-cell diff-line-hunk" colSpan={3}>{row.left}</td>
              </tr>
            );
          }
          return (
            <tr key={idx} className="diff-split-row">
              <td className="diff-split-gutter diff-split-gutter-left">
                {row.leftNum != null ? row.leftNum : ''}
              </td>
              <td className={`diff-split-cell diff-split-left ${row.leftClass || ''}`}>
                <span className="diff-split-text">{row.left || '\u00A0'}</span>
              </td>
              <td className="diff-split-gutter diff-split-gutter-right">
                {row.rightNum != null ? row.rightNum : ''}
              </td>
              <td className={`diff-split-cell diff-split-right ${row.rightClass || ''}`}>
                <span className="diff-split-text">{row.right || '\u00A0'}</span>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
