CREATE TABLE profiles (
    user_id text PRIMARY KEY CHECK (user_id ~ '^[A-Za-z0-9_-]{1,32}$'),
    active boolean NOT NULL,
    interests jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(interests) = 'array' AND NOT jsonb_path_exists(interests, '$[*] ? (@.type() != "string")')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0)
);

CREATE TABLE interaction_pairs (
    user_low text NOT NULL REFERENCES profiles(user_id),
    user_high text NOT NULL REFERENCES profiles(user_id),
    blocked_by text REFERENCES profiles(user_id),
    PRIMARY KEY (user_low, user_high),
    CHECK (user_low < user_high),
    CHECK (blocked_by IS NULL OR blocked_by IN (user_low, user_high))
);

CREATE TABLE reactions (
    source_id text NOT NULL REFERENCES profiles(user_id),
    target_id text NOT NULL REFERENCES profiles(user_id),
    status text NOT NULL CHECK (status IN ('pending', 'accepted')),
    pair_low text GENERATED ALWAYS AS (LEAST(source_id, target_id)) STORED,
    pair_high text GENERATED ALWAYS AS (GREATEST(source_id, target_id)) STORED,
    PRIMARY KEY (source_id, target_id),
    FOREIGN KEY (pair_low, pair_high) REFERENCES interaction_pairs(user_low, user_high),
    CHECK (source_id <> target_id)
);

CREATE TABLE matches (
    user_low text NOT NULL,
    user_high text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    deactivated_at timestamptz,
    PRIMARY KEY (user_low, user_high),
    FOREIGN KEY (user_low, user_high) REFERENCES interaction_pairs(user_low, user_high),
    CHECK (user_low < user_high),
    CHECK ((active AND deactivated_at IS NULL) OR (NOT active AND deactivated_at IS NOT NULL))
);
