# This jq script is expected to be called with "-s" and using 2 yaml file as command line args

# 1. jq merges both files into one single array with the contents, we assume they are both the same length
(. | length / 2) as $num_crds |

# 2. Convert the array into 2 maps, indexed by CRD names. This will 
(.[:$num_crds] | map({key: .metadata.name, value: .}) | from_entries) as $orig |
(.[$num_crds:] | map({key: .metadata.name, value: .}) | from_entries) as $aux |

# 4. Copy every leaf description field onto the original structure, then finally go back to an array of CRDs
$aux | [paths] |
    map(select(
        # description fields
        .[-1] == "description" or
        # Special cases for top-level common values
        .[-3:] == ["openAPIV3Schema", "properties", "apiVersion"] or
        .[-3:] == ["openAPIV3Schema", "properties", "kind"] or
        .[-4:] == ["openAPIV3Schema", "properties", "metadata", "type"])) |
    map(. as $p | {path: $p , value: $aux | getpath($p)}) as $desc_spec |
reduce $desc_spec[] as $s ($orig; setpath($s.path; $s.value)) |
    to_entries | sort_by(.key) | .[].value
