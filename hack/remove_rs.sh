#/bin/bash

SA_NAME="appstudio-pipeline"
IFS=$'\n' LIST=( $(kubectl get remotesecrets -L appstudio.redhat.com/internal -A -o custom-columns=":metadata.namespace,:metadata.name" --no-headers | grep "\-pull\|\-push") )
IFS=' '

for QN in "${LIST[@]}"; do
    read -ra ARR <<< "$QN"
    NS=${ARR[0]}
    NAME=${ARR[1]}

    # read remotesecret json
    RSJSON=$(kubectl get remotesecret "${NAME}" -n ${NS} -o json)

    # preserve ownerReferences
    OWNREF=$(echo $RSJSON | jq '[ .metadata.ownerReferences[]]')
    OREF_PATCH="{\"metadata\":{\"ownerReferences\":$OWNREF}}"
    # echo $OREF_PATCH

    # read secret json
    SJSON=$(kubectl get secret ${NAME} -n ${NS} -o json)
    if [[ -z $SJSON ]]; then
      echo "Secret $NS/$NAME not found. Data not exists? Continue."
      continue
    fi

    # delete remotesecret
    if kubectl delete remotesecret ${NAME} -n ${NS}
    then
      echo "RS $NS/$NAME removed OK"
    else
      echo "Failed to delete RemoteSecret $NS/$NAME. Exiting"
      exit 1
    fi

    sleep 3

     # re-create secret from json
   if echo $SJSON | kubectl create -f -
    then
      echo "Secret $NS/$NAME re-created OK"
    else
      echo "Failed to re-create Secret $NS/$NAME. Exiting"
      exit 1
    fi

    #patch service account for secrets
    if [[ $NAME == *-push ]]; then
      if kubectl patch serviceaccount $SA_NAME -n $NS -p '{"secrets": [{"name": "'$NAME'"}], "imagePullSecrets": [{"name": "'$NAME'"}]}' --type="merge"
      then
        echo "Service account $NS/$SA_NAME patched OK"
      else
        echo "Failed to patch Service account $NS/$SA_NAME. Exiting"
        exit 1
      fi
    fi

    # patch ownerReferences in the secret by saved one
    if kubectl patch secret ${NAME} -n ${NS} -p "${OREF_PATCH}" --type="merge"
    then
      echo "Secret $NS/$NAME owner ref patched OK"
    else
      echo "Failed to patch Secret $NS/$NAME owner ref. Exiting"
      exit 1
    fi

    # remove redundant labels/annotations
    kubectl label secret $NAME -n $NS appstudio.redhat.com/linked-by-remote-secret-
    kubectl annotate secret $NAME -n $NS appstudio.redhat.com/linked-remote-secrets-
    kubectl annotate secret $NAME -n $NS appstudio.redhat.com/managing-remote-secret-
done