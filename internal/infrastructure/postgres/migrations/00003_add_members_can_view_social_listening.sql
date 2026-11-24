-- Copyright The Linux Foundation and each contributor to LFX.
-- SPDX-License-Identifier: MIT

-- +goose Up
-- +goose StatementBegin

-- model.CommitteeSettings.MembersCanViewSocialListening: off unless a group opts in.
ALTER TABLE committee_settings
    ADD COLUMN members_can_view_social_listening BOOLEAN NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE committee_settings DROP COLUMN IF EXISTS members_can_view_social_listening;
-- +goose StatementEnd
