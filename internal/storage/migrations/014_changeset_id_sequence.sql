CREATE SEQUENCE IF NOT EXISTS changeset_id_seq START WITH 1 INCREMENT BY 1;

SELECT setval(
    'changeset_id_seq',
    GREATEST(
        COALESCE(
            (
                SELECT MAX(
                    COALESCE(
                        (regexp_match(id, '^cs-global-([0-9]+)$'))[1]::bigint,
                        (regexp_match(id, '^cs-([0-9]+)$'))[1]::bigint
                    )
                )
                FROM changesets
                WHERE id ~ '^cs-global-[0-9]+$' OR id ~ '^cs-[0-9]+$'
            ),
            0
        ) + 1,
        1
    ),
    false
);
