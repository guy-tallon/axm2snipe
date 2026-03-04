#!/bin/bash
# Creates custom fields in Snipe-IT for ABM/AppleCare data and associates them with a fieldset.
# Edit SNIPE_URL, API_KEY, and FIELDSET_ID before running.
# Uncomment the create_field lines for the fields you need.

set -euo pipefail

SNIPE_URL="https://your-instance.snipe-it.io"
API_KEY="your-snipe-it-api-key"
FIELDSET_ID=1

create_field() {
    local name="$1"
    local element="${2:-text}"
    local format="${3:-ANY}"
    local field_values="${4:-}"

    echo "Creating field: $name (element=$element, format=$format)"
    local payload="{\"name\":\"$name\",\"element\":\"$element\",\"format\":\"$format\",\"field_encrypted\":false"
    if [ -n "$field_values" ]; then
        payload="$payload,\"field_values\":\"$field_values\""
    fi
    payload="$payload}"

    response=$(curl -s -X POST "$SNIPE_URL/api/v1/fields" \
        -H "Authorization: Bearer $API_KEY" \
        -H "Accept: application/json" \
        -H "Content-Type: application/json" \
        -d "$payload")

    echo "  Response: $response"

    # Extract the field ID from the response
    field_id=$(echo "$response" | python3 -c "import sys,json; print(json.load(sys.stdin).get('payload',{}).get('id',''))" 2>/dev/null || true)

    if [ -z "$field_id" ]; then
        echo "  WARNING: Could not extract field ID, skipping fieldset association"
        return
    fi

    echo "  Created field ID: $field_id"

    # Associate with fieldset
    echo "  Associating with fieldset $FIELDSET_ID..."
    assoc_response=$(curl -s -X POST "$SNIPE_URL/api/v1/fieldsets/$FIELDSET_ID/fields" \
        -H "Authorization: Bearer $API_KEY" \
        -H "Accept: application/json" \
        -H "Content-Type: application/json" \
        -d "{\"field_id\":$field_id}")

    echo "  Associate response: $assoc_response"
    echo ""
}

update_field() {
    local field_id="$1"
    local element="$2"
    local format="${3:-ANY}"
    local field_values="${4:-}"

    echo "Updating field ID $field_id (element=$element, format=$format)"
    local payload="{\"element\":\"$element\",\"format\":\"$format\""
    if [ -n "$field_values" ]; then
        payload="$payload,\"field_values\":\"$field_values\""
    fi
    payload="$payload}"

    response=$(curl -s -X PATCH "$SNIPE_URL/api/v1/fields/$field_id" \
        -H "Authorization: Bearer $API_KEY" \
        -H "Accept: application/json" \
        -H "Content-Type: application/json" \
        -d "$payload")

    echo "  Response: $response"
    echo ""
}

echo "=== Creating custom fields in Snipe-IT ==="
echo ""

# Uncomment the fields you want to create:

# AppleCare Status - radio with known ABM status values
# create_field "AppleCare Status" "radio" "ANY" "Active\nInactive\nExpired"

# AppleCare Description - free text
# create_field "AppleCare Description" "text" "ANY"

# AppleCare Start Date - date format
# create_field "AppleCare Start Date" "text" "DATE"

# Warranty End Date - date format
# create_field "Warranty End Date" "text" "DATE"

# AppleCare Renewable - listbox (boolean)
# create_field "AppleCare Renewable" "listbox" "BOOLEAN" "true\nfalse"

# AppleCare Payment Type - radio with known values
# create_field "AppleCare Payment Type" "radio" "ANY" "Paid Up Front\nFree\nIncluded\nNone"

# Assigned MDM Server - free text
# create_field "Assigned MDM Server" "text" "ANY"

echo "=== Done ==="
echo ""
echo "Add the db_column_name values from the responses above to settings.yaml field_mapping."
echo ""

# --- To update EXISTING fields to correct types, uncomment and adjust IDs below ---
# update_field <field_id> "radio" "ANY" "Active\nInactive\nExpired"      # AppleCare Status
# update_field <field_id> "text" "DATE"                                   # AppleCare Start Date
# update_field <field_id> "text" "DATE"                                   # Warranty End Date
# update_field <field_id> "listbox" "BOOLEAN" "true\nfalse"              # AppleCare Renewable
# update_field <field_id> "radio" "ANY" "Paid Up Front\nFree\nIncluded\nNone"  # AppleCare Payment Type
