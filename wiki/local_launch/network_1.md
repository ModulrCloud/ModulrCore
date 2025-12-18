### 1. Clone repo

```
git clone https://github.com/modulrcloud/modulr-core.git

cd ModulrCore
```

### 2. Build it

```
chmod 700 build.sh

./build.sh
```

## 3. Prepare chaindata dir

```
mkdir XTESTNET_1 # might be done even in ModulrCore directory

mkdir XTESTNET_1/V1 # directory for validator 1
```


## 4. Move genesis and configs to chaindata dir

```
cp templates/testnet_1/* XTESTNET_1/V1
```

## 5. Set the path to chaindata dir

```
export CHAINDATA_PATH=/full/path/to/XTESTNET_1/V1
```

## 6. Run python script to update the first epoch timestamp

```
python testnet_update.py /full/path/to/XTESTNET_1
```

## 7. Finally - run the binary

```
./modulr
```

<br>

# Stop and restart network

```
Ctrl+C
```

And then just run same python script

```
python testnet_update.py /full/path/to/XTESTNET_1
```

And run binary again

```
./modulr
```